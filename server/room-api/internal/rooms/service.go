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
	"time"
)

var ErrInvalidRoom = errors.New("room not found")
var ErrRoomClosed = errors.New("room closed")
var ErrCloseForbidden = errors.New("owner token mismatch")
var ErrOperationInProgress = errors.New("room operation is already in progress")

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
	return &Service{store: s, nb: nb, cfg: cfg}
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
			if err := s.store.ResetOperation(ctx, idempotencyKey, roomID); err != nil {
				return RoomResponse{}, err
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
		return RoomResponse{}, err
	}
	if err := s.store.CreateRoom(ctx, store.Room{
		ID: roomID, CodeHash: roomcrypto.Hash(code), CodeCiphertext: codeCiphertext,
		Status: "creating", CreatedAt: time.Now().UTC(),
	}); err != nil {
		return RoomResponse{}, err
	}
	if err := s.store.SetOwnerTokenHash(ctx, roomID, ownerTokenHash); err != nil {
		return RoomResponse{}, err
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
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create setup key: %w", err))
	}
	keyCiphertext, err := roomcrypto.Seal(s.cfg.EncryptionKey, []byte(key.Key))
	if err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("encrypt setup key: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, "", keyCiphertext); err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save resources: %w", err))
	}
	policy, err := s.nb.CreateRoomPolicy(ctx, resourceName+"-internal", group.ID)
	if err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create policy: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, policy.ID, keyCiphertext); err != nil {
		_ = s.nb.DeletePolicy(ctx, policy.ID)
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		_ = s.nb.DeleteGroup(ctx, group.ID)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save policy: %w", err))
	}
	if err := s.store.SetStatus(ctx, roomID, "active", ""); err != nil {
		return RoomResponse{}, err
	}
	response := RoomResponse{RoomID: roomID, RoomCode: code, ManagementURL: s.cfg.ManagementURL, SetupKey: key.Key, RelayEnabled: s.cfg.RelayEnabled, OwnerToken: ownerToken}
	if idempotencyKey != "" {
		clear, _ := json.Marshal(response)
		ciphertext, sealErr := roomcrypto.Seal(s.cfg.EncryptionKey, clear)
		if sealErr != nil {
			return RoomResponse{}, sealErr
		}
		if err := s.store.SaveOperation(ctx, idempotencyKey, ciphertext, "active"); err != nil {
			return RoomResponse{}, err
		}
	}
	audit.Event("room_created", map[string]any{"room_id": roomID, "group_id": group.ID})
	return response, nil
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

func (s *Service) Reconcile(ctx context.Context) error {
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
	return nil
}

func (s *Service) fail(ctx context.Context, roomID, idempotencyKey string, err error) (RoomResponse, error) {
	_ = s.store.SetStatus(ctx, roomID, "error", err.Error())
	if idempotencyKey != "" {
		_ = s.store.SaveOperation(ctx, idempotencyKey, nil, "error")
	}
	audit.Event("room_provision_failed", map[string]any{"room_id": roomID, "error": err.Error()})
	return RoomResponse{}, err
}

// lookupActiveRoom 按房间码查房间；区分"不存在"与"已关闭"。
func (s *Service) lookupActiveRoom(ctx context.Context, code string) (store.Room, error) {
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
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

func (s *Service) Peers(ctx context.Context, code string) (PeerResponse, error) {
	room, err := s.lookupActiveRoom(ctx, code)
	if err != nil {
		return PeerResponse{}, err
	}
	peers, err := s.nb.ListPeers(ctx)
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
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidRoom
	}
	if err != nil {
		return err
	}
	if room.Status == "closed" {
		return nil // 幂等
	}
	if err := verifyOwnerToken(room, ownerToken); err != nil {
		return err
	}
	return s.teardownRoom(ctx, room, "owner_left")
}

// Heartbeat 房主心跳：验证令牌并刷新存活时间。房间已关闭时返回 ErrRoomClosed，
// 让房主客户端尽快停止心跳并转本地无房态。
func (s *Service) Heartbeat(ctx context.Context, code, ownerToken string) error {
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) {
		return ErrInvalidRoom
	}
	if err != nil {
		return err
	}
	if room.Status == "closed" || room.Status == "closing" {
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

func (s *Service) sweepOwnerOffline(ctx context.Context, offlineAfter time.Duration) {
	cutoff := time.Now().UTC().Add(-offlineAfter)
	rooms, err := s.store.ListOwnerSweepCandidates(ctx, cutoff)
	if err != nil {
		audit.Event("room_owner_sweep_failed", map[string]any{"error": err.Error()})
		return
	}
	for _, room := range rooms {
		if err := s.teardownRoom(ctx, room, "owner_offline"); err != nil {
			audit.Event("room_owner_sweep_close_failed", map[string]any{"room_id": room.ID, "error": err.Error()})
		}
	}
}

// teardownRoom 执行实际的资源回收。任一步失败会把状态还原回 active（允许重试），
// 全部成功才进入 closed。NetBird 侧的删除动作天然幂等，重复执行安全。
func (s *Service) teardownRoom(ctx context.Context, room store.Room, reason string) error {
	if err := s.store.SetStatus(ctx, room.ID, "closing", ""); err != nil {
		return err
	}
	var failures []string
	if room.PolicyID != "" {
		if err := s.nb.DeletePolicy(ctx, room.PolicyID); err != nil {
			failures = append(failures, "delete policy: "+err.Error())
		}
	}
	if room.SetupKeyID != "" {
		if err := s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID}); err != nil {
			failures = append(failures, "revoke setup key: "+err.Error())
		}
		if err := s.nb.DeleteSetupKey(ctx, room.SetupKeyID); err != nil {
			failures = append(failures, "delete setup key: "+err.Error())
		}
	}
	// 强制断开组内所有 peer（这是让成员真正掉线的手段）
	if room.GroupID != "" {
		peers, err := s.nb.ListPeers(ctx)
		if err != nil {
			failures = append(failures, "list peers: "+err.Error())
		} else {
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
					failures = append(failures, "delete peer "+peer.ID+": "+err.Error())
				}
			}
		}
		if err := s.nb.DeleteGroup(ctx, room.GroupID); err != nil {
			failures = append(failures, "delete group: "+err.Error())
		}
	}
	if len(failures) > 0 {
		message := strings.Join(failures, "; ")
		_ = s.store.SetStatus(ctx, room.ID, "active", message)
		return fmt.Errorf("close room failed (will remain active): %s", message)
	}
	if err := s.store.MarkRoomClosed(ctx, room.ID, reason, time.Now().UTC()); err != nil {
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
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) || err != nil {
		return ErrInvalidRoom
	}
	if room.Status == "disabled" {
		return nil
	}
	if err := s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID}); err != nil {
		return err
	}
	if room.PolicyID != "" {
		if err := s.nb.DeletePolicy(ctx, room.PolicyID); err != nil {
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

func decrypt(key []byte, ciphertext []byte) ([]byte, error) {
	return roomcrypto.Open(key, ciphertext)
}