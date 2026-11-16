// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

package rooms

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sogame/server/room-api/internal/audit"
	roomcrypto "sogame/server/room-api/internal/crypto"
	"sogame/server/room-api/internal/netbird"
	"sogame/server/room-api/internal/store"
	"strings"
	"sync"
	"time"
)

var ErrInvalidRoom = errors.New("room not found")
var ErrRoomClosed = errors.New("room closed")
var ErrCloseForbidden = errors.New("owner token mismatch")
var ErrOperationInProgress = errors.New("room operation is already in progress")
var ErrRoomGone = errors.New("room has been closed")

const (
	// ownerSweepWorkers 房主离线看门狗每次清扫的并发回收 worker 数。
	ownerSweepWorkers = 4
	// ownerSweepBudget 单次清扫的总超时预算；防止积压房间时一次 sweep 长时间阻塞后续 ticker。
	ownerSweepBudget = 30 * time.Second
	// zombieRoomGrace 僵尸房间（creating/error 且从未交付）的宽限期：
	// 超过该时长仍未交付即由启动恢复回收其 NetBird 资源。
	// 必须显著大于正常建房耗时（秒级），取 10 分钟。
	zombieRoomGrace = 10 * time.Minute
	// peersCacheTTL 全账号 peer 列表的缓存时长：Peers 轮询（每房间每成员
	// 5s）与 teardown 共用，避免 Management QPS 随 房间数×成员数 线性放大。
	// 成员列表秒级延迟可接受。
	peersCacheTTL = 2 * time.Second
)

type NetBirdAPI interface {
	ListGroups(context.Context) ([]netbird.Group, error)
	CreateGroup(context.Context, string) (netbird.Group, error)
	DeleteGroup(context.Context, string) error
	ListSetupKeys(context.Context) ([]netbird.SetupKey, error)
	CreateSetupKey(context.Context, string, string) (netbird.SetupKeyClear, error)
	RevokeSetupKey(context.Context, string, []string) error
	DeleteSetupKey(context.Context, string) error
	ListPolicies(context.Context) ([]netbird.Policy, error)
	CreateRoomPolicy(context.Context, string, string) (netbird.Policy, error)
	DeletePolicy(context.Context, string) error
	DisablePolicy(context.Context, netbird.Policy) error
	ListPeers(context.Context) ([]netbird.Peer, error)
	// DeletePeer 强制删除 peer（强制其客户端下线）；房间解散时用于成员断连。
	DeletePeer(context.Context, string) error
}

type Config struct {
	ManagementURL string
	EncryptionKey []byte
	// CodePepper 房间码 HMAC 查询键的 pepper（见 crypto.HashCode）。
	// 为空时由 EncryptionKey 域分离派生（部署零新增变量）；
	// 显式配置（ROOM_API_CODE_PEPPER）可独立轮换。
	CodePepper []byte
	// RelayEnabled 表示该服务器是否允许房间使用 Relay 中继。
	// 由提供服务的服务器掌握，随房间 enrollment 下发给客户端：
	//   false（默认）→ 纯 P2P 优先
	//   true → 允许中继回退
	RelayEnabled bool
	// 房主离线自动关闭：房主心跳超过该时长未更新即由看门狗解散房间。
	OwnerOfflineAfter time.Duration
	// 看门狗扫描间隔。
	SweepInterval time.Duration
}

type Service struct {
	store *store.Store
	nb    NetBirdAPI
	cfg   Config
	// peersMu/peersCache/peersFetchedAt 全账号 peer 列表的短 TTL 缓存。
	peersMu        sync.Mutex
	peersCache     []netbird.Peer
	peersFetchedAt time.Time
}

type RoomResponse struct {
	RoomID        string `json:"room_id"`
	RoomCode      string `json:"room_code,omitempty"`
	ManagementURL string `json:"management_url"`
	SetupKey      string `json:"setup_key"`
	// RelayEnabled 随房间下发给客户端：false 表示该服务器不允许中继（纯 P2P）。
	RelayEnabled bool `json:"relay_enabled"`
	// OwnerToken 房主令牌：仅在 Create 响应（及其幂等重放）中下发明文一次；
	// 服务端只保存其 SHA256。持有者可调用 /rooms/{code}/close 解散房间。
	OwnerToken string `json:"owner_token,omitempty"`
}

type PeerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IP        string `json:"netbird_ip"`
	Connected bool   `json:"connected"`
	Hostname  string `json:"hostname,omitempty"`
}

type PeerResponse struct {
	RoomID string     `json:"room_id"`
	Peers  []PeerView `json:"peers"`
}

func New(s *store.Store, nb NetBirdAPI, cfg Config) *Service {
	if len(cfg.CodePepper) == 0 {
		cfg.CodePepper = DeriveCodePepper(cfg.EncryptionKey, "")
	}
	return &Service{store: s, nb: nb, cfg: cfg}
}

// DeriveCodePepper 派生房间码 HMAC 的 pepper：显式值（ROOM_API_CODE_PEPPER，
// 可独立轮换）优先；未配置时从加密密钥域分离派生，避免与密文密钥混用。
func DeriveCodePepper(encryptionKey []byte, explicit string) []byte {
	if explicit != "" {
		sum := sha256.Sum256([]byte("room-code-pepper-v1|explicit|" + explicit))
		return sum[:]
	}
	mac := hmac.New(sha256.New, encryptionKey)
	mac.Write([]byte("room-code-pepper-v1|derived"))
	return mac.Sum(nil)
}

func (s *Service) Create(ctx context.Context, idempotencyKey string) (RoomResponse, error) {
	retryOperation := false
	if idempotencyKey != "" {
		op, err := s.store.GetOperation(ctx, idempotencyKey)
		if err == nil {
			if op.Status == "creating" {
				return RoomResponse{}, ErrOperationInProgress
			}
			if op.Status == "error" {
				retryOperation = true
			} else {
				var response RoomResponse
				clear, openErr := roomcrypto.Open(s.cfg.EncryptionKey, op.Response)
				if openErr != nil {
					return RoomResponse{}, fmt.Errorf("open idempotent response: %w", openErr)
				}
				if err := json.Unmarshal(clear, &response); err != nil {
					return RoomResponse{}, fmt.Errorf("decode idempotent response: %w", err)
				}
				return response, nil
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return RoomResponse{}, err
		}
	}

	roomID, err := randomID()
	if err != nil {
		return RoomResponse{}, err
	}
	code, err := randomRoomCode()
	if err != nil {
		return RoomResponse{}, err
	}
	ownerToken, ownerTokenHash, err := newOwnerToken()
	if err != nil {
		return RoomResponse{}, err
	}
	if idempotencyKey != "" {
		if retryOperation {
			// 并发重试同一幂等键时只有一个能抢到重置权（CAS）；
			// 抢不到者必须放弃，否则会与持有者双双执行完整建房流程，
			// 产出一套永不被回收的孤儿房间与 NetBird 资源。
			reset, err := s.store.ResetOperation(ctx, idempotencyKey, roomID)
			if err != nil {
				return RoomResponse{}, err
			}
			if !reset {
				return RoomResponse{}, ErrOperationInProgress
			}
		}
		created, err := s.store.BeginOperation(ctx, idempotencyKey, roomID)
		if err != nil {
			return RoomResponse{}, err
		}
		if !created && !retryOperation {
			return RoomResponse{}, ErrOperationInProgress
		}
	}
	codeCiphertext, err := roomcrypto.Seal(s.cfg.EncryptionKey, []byte(code))
	if err != nil {
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("seal room code: %w", err))
	}
	// code_hash UNIQUE 冲突（概率 ~2^-60）时重生成房间码重试一次，
	// 避免用户看到一次莫名的 502。
	for attempt := 0; ; attempt++ {
		err = s.store.CreateRoom(ctx, store.Room{
			ID: roomID, CodeHash: roomcrypto.HashCode(s.cfg.CodePepper, code), CodeCiphertext: codeCiphertext,
			Status: "creating", CreatedAt: time.Now().UTC(), OwnerTokenHash: ownerTokenHash,
		})
		if err == nil {
			break
		}
		if attempt == 0 && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			code, err = randomRoomCode()
			if err != nil {
				return s.fail(ctx, roomID, idempotencyKey, err)
			}
			codeCiphertext, err = roomcrypto.Seal(s.cfg.EncryptionKey, []byte(code))
			if err != nil {
				return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("seal room code: %w", err))
			}
			continue
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create room record: %w", err))
	}

	resourceName := "room-" + roomID
	group, found, err := s.findGroup(ctx, resourceName)
	createdGroup := false
	if err == nil && !found {
		group, err = s.nb.CreateGroup(ctx, resourceName)
		createdGroup = err == nil
	}
	if err != nil {
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create group: %w", err))
	}
	key, err := s.nb.CreateSetupKey(ctx, resourceName, group.ID)
	if err != nil {
		s.compensateProvisioning(ctx, "", "", group.ID, createdGroup)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create setup key: %w", err))
	}
	keyCiphertext, err := roomcrypto.Seal(s.cfg.EncryptionKey, []byte(key.Key))
	if err != nil {
		s.compensateProvisioning(ctx, "", key.ID, group.ID, createdGroup)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("encrypt setup key: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, "", keyCiphertext); err != nil {
		s.compensateProvisioning(ctx, "", key.ID, group.ID, createdGroup)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save resources: %w", err))
	}
	policy, err := s.nb.CreateRoomPolicy(ctx, resourceName+"-internal", group.ID)
	if err != nil {
		s.compensateProvisioning(ctx, "", key.ID, group.ID, createdGroup)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create policy: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, policy.ID, keyCiphertext); err != nil {
		s.compensateProvisioning(ctx, policy.ID, key.ID, group.ID, createdGroup)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save policy: %w", err))
	}
	if err := s.store.SetStatus(ctx, roomID, "active", ""); err != nil {
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("activate room: %w", err))
	}
	response := RoomResponse{RoomID: roomID, RoomCode: code, ManagementURL: s.cfg.ManagementURL, SetupKey: key.Key, RelayEnabled: s.cfg.RelayEnabled, OwnerToken: ownerToken}
	if idempotencyKey != "" {
		clear, _ := json.Marshal(response)
		ciphertext, sealErr := roomcrypto.Seal(s.cfg.EncryptionKey, clear)
		if sealErr != nil {
			// 房间从未交付（Create 返回错误），标 error 由启动恢复的
			// 僵尸清理兜底回收其 NetBird 资源，不能留在 active。
			return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("seal idempotent response: %w", sealErr))
		}
		if err := s.store.SaveOperation(ctx, idempotencyKey, ciphertext, "active"); err != nil {
			return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save idempotent response: %w", err))
		}
	}
	audit.Event("room_created", map[string]any{"room_id": roomID, "group_id": group.ID})
	return response, nil
}

// compensateProvisioning 建房事务的 NetBird 侧补偿（全部失败路径共用）：
// 删除 policy → 吊销并删除 setup key → 按 createdGroup 条件删除 group。
//   - setup key 必须 Revoke+Delete 双管齐下：只吊销会让 revoked key 垃圾
//     永久残留账号、稀释配额并拖慢管理面 List。
//   - createdGroup=false 时 group 是 findGroup 复用的既有资源（上次失败
//     残留），不在本次补偿范围，绝不能删。
// 删除失败仅忽略（与原语义一致）：残留由启动恢复的僵尸清理兜底。
func (s *Service) compensateProvisioning(ctx context.Context, policyID, setupKeyID, groupID string, createdGroup bool) {
	if policyID != "" {
		_ = s.nb.DeletePolicy(ctx, policyID)
	}
	if setupKeyID != "" {
		_ = s.nb.RevokeSetupKey(ctx, setupKeyID, []string{groupID})
		_ = s.nb.DeleteSetupKey(ctx, setupKeyID)
	}
	if createdGroup && groupID != "" {
		_ = s.nb.DeleteGroup(ctx, groupID)
	}
}

func (s *Service) findGroup(ctx context.Context, name string) (netbird.Group, bool, error) {
	groups, err := s.nb.ListGroups(ctx)
	if err != nil {
		return netbird.Group{}, false, err
	}
	for _, group := range groups {
		if group.Name == name {
			return group, true, nil
		}
	}
	return netbird.Group{}, false, nil
}

// Reconcile 启动恢复：进程重启后把所有中断/残留状态收口。分四个阶段，
// 每个阶段幂等，任何阶段失败即返回（下一次启动重试）。
func (s *Service) Reconcile(ctx context.Context) error {
	// 阶段零：隔离自检。账号级 Default all-to-all policy 若仍 enabled，
	// 所有房间的 peer 在数据面全账号互通——房间隔离静默失效。
	s.warnIfDefaultPolicyEnabled(ctx)

	// 阶段一：creating 状态的幂等操作收口（进程在 NetBird 调用中途退出）。
	operations, err := s.store.ListOperationsByStatus(ctx, "creating")
	if err != nil {
		return err
	}
	for _, operation := range operations {
		room, err := s.store.GetRoom(ctx, operation.RoomID)
		if errors.Is(err, store.ErrNotFound) {
			if err := s.store.SaveOperation(ctx, operation.IdempotencyKey, nil, "error"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if room.Status == "active" {
			// 建房其实已完整成功（Create 尾部 SaveOperation/Seal 失败留下的
			// "active 房间 + creating operation" 残留）：只把 operation 标记为
			// error，绝不能删除仍在服役的 NetBird 资源。
			if err := s.store.SaveOperation(ctx, operation.IdempotencyKey, nil, "error"); err != nil {
				return err
			}
			continue
		}
		if room.PolicyID != "" {
			_ = s.nb.DeletePolicy(ctx, room.PolicyID)
		}
		if room.SetupKeyID != "" {
			_ = s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID})
			_ = s.nb.DeleteSetupKey(ctx, room.SetupKeyID)
		}
		if room.GroupID != "" {
			_ = s.nb.DeleteGroup(ctx, room.GroupID)
		}
		if err := s.store.SetStatus(ctx, room.ID, "error", "provisioning interrupted and reconciled"); err != nil {
			return err
		}
		if err := s.store.SaveOperation(ctx, operation.IdempotencyKey, nil, "error"); err != nil {
			return err
		}
		audit.Event("room_provision_reconciled", map[string]any{"room_id": room.ID})
	}

	// 阶段二：卡在 closing 的房间重排队。进程重启意味着在途 teardown
	// 已中断（teardown 中途 ctx 取消/进程退出都会留下 closing 僵尸，
	// 而看门狗只扫 active、Close 重试被 CAS 幂等吞掉——无任何恢复路径）。
	// 拨回 active 且心跳置 0 后，看门狗下一拍即按标准路径回收。
	requeued, err := s.store.RequeueClosingRooms(ctx)
	if err != nil {
		return err
	}
	if requeued > 0 {
		audit.Event("rooms_requeued_from_closing", map[string]any{"count": requeued})
	}

	// 阶段三：僵尸房间（creating/error 且超时）清理。这些房间从未交付
	// 给任何客户端（无幂等键崩溃残留、或建房失败后重试已另建新房），
	// 其 NetBird 资源必须回收，否则永久泄漏。
	zombieBefore := time.Now().Add(-zombieRoomGrace).UnixNano()
	zombies, err := s.store.ListZombieRooms(ctx, zombieBefore)
	if err != nil {
		return err
	}
	for _, room := range zombies {
		if room.PolicyID != "" {
			_ = s.nb.DeletePolicy(ctx, room.PolicyID)
		}
		if room.SetupKeyID != "" {
			_ = s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID})
			_ = s.nb.DeleteSetupKey(ctx, room.SetupKeyID)
		}
		if room.GroupID != "" {
			_ = s.nb.DeleteGroup(ctx, room.GroupID)
		}
		if err := s.store.MarkRoomClosed(ctx, room.ID, "zombie_reaped", time.Now().UTC()); err != nil {
			return err
		}
		audit.Event("room_zombie_reaped", map[string]any{"room_id": room.ID})
	}

	// 阶段四：stale 房间（心跳超时）启动回收。与看门狗同一阈值（可配置的
	// OwnerOfflineAfter，而非独立的硬编码值——调大配置后重启不得误杀
	// 正常房间）、同一路径（teardownRoom：closed 语义 + 强制踢 peer，
	// 而非 Disable 的 404 语义——成员客户端需要 410 才能自动清理本地状态）。
	if s.cfg.OwnerOfflineAfter > 0 {
		staleBefore := time.Now().Add(-s.cfg.OwnerOfflineAfter).UnixNano()
		staleRooms, err := s.store.ListStaleActiveRooms(ctx, staleBefore)
		if err != nil {
			return err
		}
		for _, room := range staleRooms {
			if err := s.teardownRoom(ctx, room, "stale_reaped"); err != nil {
				// 失败不阻塞其余房间；房间保持 active 由看门狗下拍兜底。
				audit.Event("room_stale_reap_failed", map[string]any{"room_id": room.ID, "error": err.Error()})
			}
		}
	}
	return nil
}

// warnIfDefaultPolicyEnabled 启动自检：NetBird 账号默认存在名为 Default 的
// all-to-all 放行策略；本项目全靠每房间一个 Group+Policy 实现隔离，只要
// Default 仍 enabled，所有房间的 peer 在数据面全账号静默互通——房间隔离
// 形同虚设，且无任何用户侧可见症状。发现时显著告警；禁用需显式执行
// `room-api -disable-default-policy`（该 policy 可能被账号其他用途依赖，
// 不宜未经确认自动关闭）。
func (s *Service) warnIfDefaultPolicyEnabled(ctx context.Context) {
	policies, err := s.nb.ListPolicies(ctx)
	if err != nil {
		audit.Event("default_policy_check_failed", map[string]any{"error": err.Error()})
		return
	}
	for _, policy := range policies {
		if policy.Name == "Default" && policy.Enabled {
			audit.Event("default_policy_still_enabled", map[string]any{
				"policy_id": policy.ID,
				"warning":   "account-wide all-to-all policy is ENABLED; room isolation is NOT in effect. Run: room-api -disable-default-policy",
			})
		}
	}
}

func (s *Service) fail(ctx context.Context, roomID, idempotencyKey string, err error) (RoomResponse, error) {
	_ = s.store.SetStatus(ctx, roomID, "error", err.Error())
	if idempotencyKey != "" {
		_ = s.store.SaveOperation(ctx, idempotencyKey, nil, "error")
	}
	audit.Event("room_provision_failed", map[string]any{"room_id": roomID, "error": err.Error()})
	return RoomResponse{}, err
}

// lookupRoomByCode 按房间码查房间：优先 HMAC 查询键；未命中回退旧版
// 无盐 SHA256 查询键（旧数据），命中后在线迁移为 HMAC——迁移对调用方
// 透明，迁移失败不阻塞本次请求（下次访问重试）。
func (s *Service) lookupRoomByCode(ctx context.Context, code string) (store.Room, error) {
	code = strings.TrimSpace(code)
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.HashCode(s.cfg.CodePepper, code))
	if err == nil {
		return room, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Room{}, err
	}
	legacy, legacyErr := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(code))
	if legacyErr != nil {
		return store.Room{}, legacyErr
	}
	if migrateErr := s.store.UpdateCodeHash(ctx, legacy.ID, roomcrypto.HashCode(s.cfg.CodePepper, code)); migrateErr != nil {
		audit.Event("room_code_hash_migrate_failed", map[string]any{"room_id": legacy.ID})
	}
	return legacy, nil
}

// lookupActiveRoom 按房间码查房间；区分"不存在"与"已关闭"。
func (s *Service) lookupActiveRoom(ctx context.Context, code string) (store.Room, error) {
	room, err := s.lookupRoomByCode(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return store.Room{}, ErrInvalidRoom
	}
	if err != nil {
		return store.Room{}, err
	}
	if room.Status == "closed" || room.Status == "closing" {
		return store.Room{}, ErrRoomClosed
	}
	if room.Status != "active" {
		return store.Room{}, ErrInvalidRoom
	}
	return room, nil
}

func (s *Service) Join(ctx context.Context, code string) (RoomResponse, error) {
	room, err := s.lookupActiveRoom(ctx, code)
	if err != nil {
		return RoomResponse{}, err
	}
	key, err := roomcrypto.Open(s.cfg.EncryptionKey, room.SetupKeyCiphertext)
	if err != nil {
		return RoomResponse{}, err
	}
	audit.Event("room_joined", map[string]any{"room_id": room.ID})
	return RoomResponse{RoomID: room.ID, ManagementURL: s.cfg.ManagementURL, SetupKey: string(key), RelayEnabled: s.cfg.RelayEnabled}, nil
}

// listPeers 获取全账号 peer 列表（短 TTL 缓存）。
// 每个活跃房间的每个成员每 5s 轮询一次 Peers，若每次都向 Management
// 发起全量 GET /api/peers，QPS 会随 房间数×成员数 线性放大并拖垮
// 自托管 Management。秒级缓存把常态 QPS 压到 1/TTL。
func (s *Service) listPeers(ctx context.Context) ([]netbird.Peer, error) {
	s.peersMu.Lock()
	if s.peersCache != nil && time.Since(s.peersFetchedAt) < peersCacheTTL {
		cached := s.peersCache
		s.peersMu.Unlock()
		return cached, nil
	}
	s.peersMu.Unlock()
	peers, err := s.nb.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	s.peersMu.Lock()
	s.peersCache = peers
	s.peersFetchedAt = time.Now()
	s.peersMu.Unlock()
	return peers, nil
}

func (s *Service) Peers(ctx context.Context, code string) (PeerResponse, error) {
	room, err := s.lookupActiveRoom(ctx, code)
	if err != nil {
		return PeerResponse{}, err
	}
	peers, err := s.listPeers(ctx)
	if err != nil {
		return PeerResponse{}, err
	}
	response := PeerResponse{RoomID: room.ID, Peers: []PeerView{}}
	for _, peer := range peers {
		inRoom := false
		for _, group := range peer.Groups {
			if group.ID == room.GroupID {
				inRoom = true
				break
			}
		}
		if inRoom {
			response.Peers = append(response.Peers, PeerView{
				ID: peer.ID, Name: peer.Name, IP: peer.IP,
				Connected: peer.Connected, Hostname: peer.Hostname,
			})
		}
	}
	return response, nil
}

// Close 房主凭令牌解散房间：先删 policy / 吊销+删 setup key / 强制删除组内
// 所有 peer（成员立即掉线）/ 删 group，最后落库状态。关闭幂等：重复调用直接成功。
func (s *Service) Close(ctx context.Context, code, ownerToken string) error {
	room, err := s.lookupRoomByCode(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidRoom
	}
	if err != nil {
		return err
	}
	if room.Status == "closed" {
		return nil // 幂等
	}
	// status == "closing"（并发 Close 或看门狗正在回收同一房间）：继续走
	// teardownRoom——其 BeginClosing CAS 会失败并幂等返回成功，天然去重，
	// 不会重复删除，更不会把已关闭房间复活。
	if err := verifyOwnerToken(room, ownerToken); err != nil {
		return err
	}
	return s.teardownRoom(ctx, room, "owner_left")
}

// Heartbeat 房主心跳：验证令牌并刷新存活时间。房间已关闭时返回 ErrRoomClosed，
// 让房主客户端尽快停止心跳并转本地无房态。
func (s *Service) Heartbeat(ctx context.Context, code, ownerToken string) error {
	room, err := s.lookupRoomByCode(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidRoom
	}
	if err != nil {
		return err
	}
	// closing（看门狗/Close 的 teardown 进行中）：返回 409 而非 410。
	// 若 teardown 失败房间会回滚 active——此时房主必须仍在心跳，
	// 否则"心跳收到 410 → 客户端停跳清凭证 → teardown 回滚 → 房主永不再跳
	// → 下轮 sweep 回收"的自杀式回收链会杀死一个本可继续服役的房间。
	if room.Status == "closing" {
		return ErrOperationInProgress
	}
	if room.Status == "closed" {
		return ErrRoomClosed
	}
	if room.Status != "active" {
		return ErrInvalidRoom
	}
	if err := verifyOwnerToken(room, ownerToken); err != nil {
		return err
	}
	return s.store.TouchOwnerHeartbeat(ctx, room.ID, time.Now().UTC())
}

// StartOwnerWatchdog 周期清扫"房主离线超时"的房间。
// OwnerOfflineAfter<=0 时关闭本机制（仅显式 Close 生效）。
func (s *Service) StartOwnerWatchdog(ctx context.Context) {
	offlineAfter := s.cfg.OwnerOfflineAfter
	if offlineAfter <= 0 {
		return
	}
	interval := s.cfg.SweepInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepOwnerOffline(ctx, offlineAfter)
			}
		}
	}()
}

// sweepOwnerOffline 清扫"房主离线超时"的房间。使用有界 worker 池并发回收，
// 并对单次清扫施加总超时预算，避免大量积压房间时一次 sweep 长时间串行阻塞、
// 拖垮后续 ticker。单个 room 回收失败不影响本次 sweep 的其余房间。
func (s *Service) sweepOwnerOffline(ctx context.Context, offlineAfter time.Duration) {
	cutoff := time.Now().UTC().Add(-offlineAfter)
	candidates, err := s.store.ListOwnerSweepCandidates(ctx, cutoff)
	if err != nil {
		audit.Event("room_owner_sweep_failed", map[string]any{"error": err.Error()})
		return
	}
	if len(candidates) == 0 {
		return
	}

	// 单次 sweep 的总超时预算；批次大的时候不让回收越过下一拍。
	sweepCtx, cancel := context.WithTimeout(ctx, ownerSweepBudget)
	defer cancel()

	const workers = ownerSweepWorkers
	jobs := make(chan store.Room)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for room := range jobs {
				if sweepCtx.Err() != nil {
					// 超时后丢弃剩余任务，留待下一拍重试。
					continue
				}
				if err := s.teardownRoom(sweepCtx, room, "owner_offline"); err != nil {
					audit.Event("room_owner_sweep_close_failed", map[string]any{"room_id": room.ID, "error": err.Error()})
				}
			}
		}()
	}

dispatch:
	for _, room := range candidates {
		select {
		case jobs <- room:
		case <-sweepCtx.Done():
			// 预算耗尽：停止投递（此前分支是空 continue，注释与行为不符），
			// worker 处理完已接收的批次后退出。
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
}

// teardownRoom 执行实际的资源回收。先经 BeginClosing 做状态 CAS（仅 active→closing）：
// CAS 失败说明房间已被并发 Close/看门狗关闭，视为幂等成功直接返回，
// 杜绝"看门狗快照滞后于房主 Close"把已关闭房间复活为 active 的竞态。
// 删除类调用把 NetBird 404 视为成功（资源可能已被管理员在 dashboard 手动删除），
// 重复执行安全；其余任一步失败会把状态还原回 active（允许重试），全部成功才进入 closed。
func (s *Service) teardownRoom(ctx context.Context, room store.Room, reason string) error {
	acquired, err := s.store.BeginClosing(ctx, room.ID)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	var failures []string
	if room.PolicyID != "" {
		if err := s.nb.DeletePolicy(ctx, room.PolicyID); err != nil && !errors.Is(err, netbird.ErrNotFound) {
			failures = append(failures, "delete policy: "+err.Error())
		}
	}
	if room.SetupKeyID != "" {
		if err := s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID}); err != nil && !errors.Is(err, netbird.ErrNotFound) {
			failures = append(failures, "revoke setup key: "+err.Error())
		}
		if err := s.nb.DeleteSetupKey(ctx, room.SetupKeyID); err != nil && !errors.Is(err, netbird.ErrNotFound) {
			failures = append(failures, "delete setup key: "+err.Error())
		}
	}
	// 强制断开组内所有 peer（这是让成员真正掉线的手段）。
	// DeletePeer 失败不阻塞关闭：删 policy 已切断组内数据面、吊销 setup key
	// 已阻断新成员入网，成员客户端最终靠轮询 ErrRoomClosed(410) 自行退出。
	// 但 ListPeers 失败则无法确定成员是否仍在线，必须视为需要重试的硬失败。
	if room.GroupID != "" {
		peers, err := s.listPeers(ctx)
		if err != nil {
			failures = append(failures, "list peers: "+err.Error())
		} else {
			var kicked []string
			for _, peer := range peers {
				inRoom := false
				for _, group := range peer.Groups {
					if group.ID == room.GroupID {
						inRoom = true
						break
					}
				}
				if !inRoom {
					continue
				}
				if err := s.nb.DeletePeer(ctx, peer.ID); err != nil {
					kicked = append(kicked, peer.ID)
					audit.Event("room_close_peer_kick_failed", map[string]any{"peer_id": peer.ID, "error": err.Error()})
				}
			}
			if len(kicked) > 0 {
				// 成员未即时下线，记录警告但仍继续关闭（客户端会自行退出）。
				audit.Event("room_close_peer_kick_warn", map[string]any{"room_id": room.ID, "group_id": room.GroupID, "kicked_count": len(kicked)})
			}
		}
		if err := s.nb.DeleteGroup(ctx, room.GroupID); err != nil && !errors.Is(err, netbird.ErrNotFound) {
			failures = append(failures, "delete group: "+err.Error())
		}
	}
	if len(failures) > 0 {
		message := strings.Join(failures, "; ")
		// 状态回滚与外部 ctx 解耦：请求超时/sweep 预算耗尽取消 ctx 时，
		// 回滚必须仍能落库，否则房间永久卡在 closing 且无恢复路径。
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.store.SetStatus(writeCtx, room.ID, "active", message)
		return fmt.Errorf("close room failed (will remain active): %s", message)
	}
	// 落库与外部 ctx 解耦（同上）：NetBird 资源已删完，MarkRoomClosed
	// 必须完成，否则房间卡在 closing（资源已删但状态未收口）。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.MarkRoomClosed(writeCtx, room.ID, reason, time.Now().UTC()); err != nil {
		return err
	}
	audit.Event("room_closed", map[string]any{"room_id": room.ID, "reason": reason})
	return nil
}

// verifyOwnerToken 校验房主令牌（常数时间比较）。老房间（无令牌）视为无权关闭。
func verifyOwnerToken(room store.Room, token string) error {
	if room.OwnerTokenHash == "" {
		return ErrCloseForbidden
	}
	token = strings.TrimSpace(token)
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(room.OwnerTokenHash)) != 1 {
		return ErrCloseForbidden
	}
	return nil
}

// newOwnerToken 生成房主令牌（32 字节随机的 base64url 明文）及其 sha256 十六进制摘要。
func newOwnerToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func (s *Service) Disable(ctx context.Context, code string) error {
	room, err := s.lookupRoomByCode(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidRoom
	}
	// 红线：DB 故障等内部错误必须原样上抛（HTTP 层映射 502），
	// 不得与"房间不存在"混为同一 404——否则管理员会把"处置失败"
	// 误判为"房间已被清理"。
	if err != nil {
		return err
	}
	if room.Status == "disabled" {
		return nil
	}
	if err := s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID}); err != nil && !errors.Is(err, netbird.ErrNotFound) {
		return err
	}
	if room.PolicyID != "" {
		if err := s.nb.DeletePolicy(ctx, room.PolicyID); err != nil && !errors.Is(err, netbird.ErrNotFound) {
			return err
		}
	}
	if err := s.store.Disable(ctx, room.ID); err != nil {
		return err
	}
	audit.Event("room_disabled", map[string]any{"room_id": room.ID})
	return nil
}

func (s *Service) DisableDefaultPolicy(ctx context.Context) error {
	policies, err := s.nb.ListPolicies(ctx)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		if policy.Name == "Default" && policy.Enabled {
			if err := s.nb.DisablePolicy(ctx, policy); err != nil {
				return err
			}
			audit.Event("default_policy_disabled", map[string]any{"policy_id": policy.ID})
			return nil
		}
	}
	return nil
}

func randomID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func randomRoomCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return fmt.Sprintf("%s-%s-%s", buf[:4], buf[4:8], buf[8:]), nil
}