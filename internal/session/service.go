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

package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/logger"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

const (
	cleanupTimeout    = 5 * time.Second
	commandTimeout    = 30 * time.Second
	peerWaitTimeout   = 30 * time.Second
	// ownerHeartbeatInterval 房主心跳间隔;服务端 OwnerOfflineAfter 默认 5 分钟,
	// 一分钟一跳,容忍四次网络抖动仍不会被误清扫。
	ownerHeartbeatInterval = 60 * time.Second
	// ownerHeartbeatMaxBackoff 心跳连续失败时的最大退避间隔,防止服务端不可达时
	// 客户端以固定频率空打;封顶 2 分钟,保证即便退避也不会逼近服务端 OwnerOfflineAfter(5 分钟)。
	ownerHeartbeatMaxBackoff = 2 * time.Minute
	// roomMaxAge 定义本地保存的房间最长有效期。超过该时长后房间视为失效,
	// 不再提示恢复或允许重新连接,防止用户加入一个早已解散的房间。
	roomMaxAge = 24 * time.Hour
)

var (
	ErrRoomAlreadySaved           = errors.New("a room is already saved")
	ErrStoredStateConflict        = errors.New("saved room state is incomplete or inconsistent")
	ErrRoomExpired                = errors.New("saved room has expired")
	ErrCommandInProgress          = errors.New("a room command is already in progress")
	ErrSwitchConfirmationRequired = errors.New("switching rooms requires confirmation")
	ErrInvalidSwitchMode          = errors.New("switch mode must be create or join")
	// ErrRoomClosed 表示房间已被房主解散(或房主离线被服务端看门狗回收)。
	// 收到它的客户端应转回无房态,且不再展示"重新连接"。
	ErrRoomClosed = errors.New("room closed by owner")
)

type RoomAPI interface {
	Create(context.Context, *roomapi.CreateIntent) (roomapi.Enrollment, error)
	Join(context.Context, string) (roomapi.Enrollment, error)
}

type MetadataStorage interface {
	Load() (securestore.RoomMetadata, error)
	Save(securestore.RoomMetadata) error
	Clear() error
}

type RoomCodeStorage interface {
	Load() ([]byte, error)
	Save([]byte) error
	Clear() error
}

// OwnerTokenStorage 保存房主令牌；仅房间创建者本地有值。
type OwnerTokenStorage interface {
	Load() ([]byte, error)
	Save([]byte) error
	Clear() error
}

// OwnerAPI 是房主才能调用的服务端动作（关闭 / 心跳）。RoomAPI 的具体实现
// 若同时实现该接口,房主能力自动生效（与 PeerAPI 同款注入模式）。
type OwnerAPI interface {
	CloseRoom(ctx context.Context, roomCode, ownerToken string) error
	Heartbeat(ctx context.Context, roomCode, ownerToken string) error
}

type TransactionError struct {
	Cause           error
	CleanupFailures int
}

func (e *TransactionError) Error() string {
	if e.CleanupFailures == 0 {
		return "room enrollment transaction failed"
	}
	return fmt.Sprintf("room enrollment transaction failed; %d cleanup operations failed", e.CleanupFailures)
}

func (e *TransactionError) Unwrap() error { return e.Cause }

type Service struct {
	rooms    RoomAPI
	netbird  clientnetbird.Adapter
	metadata MetadataStorage
	codes    RoomCodeStorage
	machine  *Machine
	monitor  *clientnetbird.RecoveryMonitor
	peers    *PeerRefresher
	now      func() time.Time
	mu       sync.Mutex
	busy     bool
	// disconnected 记录用户主动断开的意图,使 View 轮询不会把
	// 断开状态误判为 WaitingForPeer/Reconnecting。
	disconnected bool
	// peerWaitStart 记录房间成员存在但隧道尚未建立的起始时间,
	// 超过 peerWaitTimeout 后状态机降级为 Reconnecting。
	peerWaitStart time.Time
	// resumePending 表示本地保存了房间,但用户尚未确认恢复。
	// 仅在应用启动时置位;在此状态下 View 保持"未加入"的 NoRoom 视图,
	// 不自动进入房间界面、不触发重连,等待用户显式"恢复"或"离开"。
	resumePending bool
	// lastLocalIP 记录最近一次观察到(或注册时)的本机虚拟 IP;
	// daemon 断开后 LocalNetBirdIP 为空,用它仍能把房间成员中的
	// 自己排除,避免断开后自己被打成"其他成员"导致人数 +1。
	lastLocalIP string
	// tokens 保存房主令牌;nil 表示该构建未启用房主能力(向后兼容)。
	tokens OwnerTokenStorage
	// ownerAPI 由 rooms 满足 OwnerAPI 时自动启用（房主关闭/心跳）。
	ownerAPI OwnerAPI
	// heartbeatCancel 停止房主心跳协程;nil 表示未在跑。
	heartbeatCancel context.CancelFunc
	// 身份自愈状态(见 repair.go):控制面死亡/房间归属漂移的防抖计数与冷却
	repairPolls       int
	repairRunning     bool
	lastRepairAttempt time.Time
	// closed 标记实例已关闭(Close 已调用):不再启动新的心跳/修复,
	// 进行中的 goroutine 自行结束后实例可被 GC。
	closed bool
}

func NewService(rooms RoomAPI, netbird clientnetbird.Adapter, metadata MetadataStorage, codes RoomCodeStorage) *Service {
	var peers *PeerRefresher
	if api, ok := rooms.(PeerAPI); ok {
		peers = NewPeerRefresher(api, codes)
	}
	service := &Service{
		rooms:    rooms,
		netbird:  netbird,
		metadata: metadata,
		codes:    codes,
		machine:  NewMachine(),
		monitor:  clientnetbird.NewRecoveryMonitor(netbird),
		peers:    peers,
		now:      time.Now,
	}
	if api, ok := rooms.(OwnerAPI); ok {
		service.ownerAPI = api
	}
	return service
}

// SetOwnerTokenStore 注入房主令牌存储；注入后房主能力（解散按钮/心跳）生效。
func (s *Service) SetOwnerTokenStore(tokens OwnerTokenStorage) {
	s.mu.Lock()
	s.tokens = tokens
	s.mu.Unlock()
}

// isOwner 当前房间是否本机创建（房主）。
func (s *Service) isOwner() bool {
	s.mu.Lock()
	tokens := s.tokens
	s.mu.Unlock()
	if tokens == nil {
		return false
	}
	token, err := tokens.Load()
	if err != nil {
		return false
	}
	isOwner := len(token) > 0
	clearBytes(token)
	return isOwner
}

type RoomViewSnapshot struct {
	Session         Snapshot
	Metadata        securestore.RoomMetadata
	RoomCodeMasked  string
	LocalNetBirdIP  string
	Peers           []roomapi.Peer
	DaemonPeers     []clientnetbird.Peer
	PeersStale      bool
	LastPeerRefresh time.Time
	// ResumePending 表示本地保存了上一次的房间,等待用户显式确认恢复。
	// 此时 Session.State 为 NoRoom,界面上不显示房间视图;上层用它
	// 驱动"检测到上次的房间"提示框。
	ResumePending bool
	// Disconnected 表示用户已主动断开(NatBird daemon 已下线但房间
	// 注册保留),上层据此把"断开"按钮切换为"重新连接"。
	Disconnected bool
	// IsOwner 表示当前房间由本机创建(房主):UI 据此把"离开"换成"解散",
	// 服务端以房主令牌校验。
	IsOwner bool
}

func (s *Service) View(ctx context.Context) (RoomViewSnapshot, error) {
	s.mu.Lock()
	busy := s.busy
	disconnected := s.disconnected
	pending := s.resumePending
	s.mu.Unlock()
	// 命令事务进行中时,存储可能处于"写一半"的中间状态。
	// 此时返回基于当前机器状态的轻量快照,避免把半态误判为
	// 持久化损坏(ErrStoredStateConflict)而挂上错误横幅。
	if busy {
		return s.viewWhileBusy(), nil
	}
	// 启动时保存了上次的房间但用户尚未确认恢复:
	// 保持"未加入"的 NoRoom 视图,不进入房间界面、不触发任何重连,
	// 直到用户显式执行恢复(Reconnect)或离开(Leave)。
	if pending {
		if _, err := s.loadSavedRoom(); err != nil {
			if errors.Is(err, ErrRoomExpired) {
				s.mu.Lock()
				s.resumePending = false
				s.mu.Unlock()
				logger.Infof("express view: saved room expired, resume prompt discarded")
				return RoomViewSnapshot{Session: s.machine.Apply(Facts{})}, nil
			}
			// 存储冲突等情形仍提示恢复,由用户决定清理方式
		}
		return RoomViewSnapshot{Session: s.machine.Apply(Facts{}), ResumePending: true}, nil
	}

	metadata, err := s.loadSavedRoom()
	if err != nil {
		if errors.Is(err, ErrRoomExpired) {
			logger.Infof("express view: saved room expired, ignored")
			return RoomViewSnapshot{Session: s.machine.Apply(Facts{})}, nil
		}
		return RoomViewSnapshot{}, err
	}
	code, err := s.codes.Load()
	if err != nil {
		return RoomViewSnapshot{}, ErrStoredStateConflict
	}
	masked := maskRoomCode(code)
	clearBytes(code)
	status, err := s.netbird.Status(ctx)
	if err != nil {
		return RoomViewSnapshot{}, err
	}
	// 记录最近一次观察到的本机虚拟 IP;daemon 断开(IP 为空)后,
	// 成员过滤仍凭它把房间里的自己排除,避免人数虚增。
	if host := ipHost(status.LocalNetBirdIP); host != "" {
		s.mu.Lock()
		s.lastLocalIP = host
		s.mu.Unlock()
	}
	facts := Facts{
		RoomSaved:         true,
		ControlPlaneReady: status.ManagementConnected && status.SignalConnected,
		MembershipKnown:   true,
		UserDisconnected:  disconnected,
		DaemonPeers:       status.Peers,
		// Relay 允许性由服务器随房间下发并持久化在元数据中，客户端被动遵循
		RelayAllowed: metadata.RelayEnabled,
	}
	view := RoomViewSnapshot{
		Session:        s.machine.Apply(facts),
		Metadata:       metadata,
		RoomCodeMasked: masked,
		LocalNetBirdIP: status.LocalNetBirdIP,
		DaemonPeers:    append([]clientnetbird.Peer(nil), status.Peers...),
		Disconnected:   disconnected,
		IsOwner:        s.isOwner(),
	}
	if s.peers != nil {
		peerSnapshot, peerErr := s.peers.Refresh(ctx)
		if peerErr != nil {
			var httpErr *roomapi.HTTPError
			if errors.As(peerErr, &httpErr) && httpErr.Code == roomapi.ErrorRoomClosed {
				// 房间已被房主解散(或被看门狗回收)。本地强制清档必须在
				// 命令互斥下执行——View 是高频只读路径,此前直接清理会与
				// 并发命令构成写-写竞态,可能把刚 enroll 成功的凭证误清。
				// busy 时本轮跳过:命令结束后下一次 View 仍会收到 410
				// 并完成清理。清理前二次校验 RoomID:慢网络调用窗口内
				// 若已完成房间切换(Create/Join/Reconnect),410 属于旧房间,
				// 不得误清新房间。
				if err := s.beginCommand(); err != nil {
					return RoomViewSnapshot{}, ErrRoomClosed
				}
				defer s.endCommand()
				if current, err := s.loadSavedRoom(); err == nil && current.RoomID == metadata.RoomID {
					s.stopOwnerHeartbeat()
					// 清理调用(Deregister/RemoveProfile 是无 per-call 超时的
					// gRPC)必须套短预算:daemon 存活但不响应时,无限阻塞会让
					// View 轮询 goroutine 卡死、busy 永久为 true,此后所有
					// 房间命令 ErrCommandInProgress 直至重启。
					clearCtx, cancelClear := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
					_, _ = s.forceClear(clearCtx)
					cancelClear()
				}
				return RoomViewSnapshot{}, ErrRoomClosed
			}
		}
		// 自愈:控制面死亡(enroll 后卡死)或房间归属漂移(房主复旧身份)
		// 都靠"同一房间码重新 Join"修复,观测防抖见 repair.go。
		// stale 快照(Refresh 失败的陈旧数据)不参与漂移判定——否则把
		// 历史成员列表误判为"本机已不在房间"而触发不必要的身份重建;
		// 传 nil 即仅保留控制面死亡判定。
		var membersForRepair []roomapi.Peer
		if !peerSnapshot.Stale {
			membersForRepair = peerSnapshot.Peers
		}
		s.maybeRepairIdentity(ctx, status, membersForRepair)
		localIP := status.LocalNetBirdIP
		if ipHost(localIP) == "" {
			s.mu.Lock()
			localIP = s.lastLocalIP
			s.mu.Unlock()
		}
		view.Peers = excludeLocalPeer(peerSnapshot.Peers, localIP)
		view.PeersStale = peerSnapshot.Stale
		view.LastPeerRefresh = peerSnapshot.LastRefreshAt
		facts.OtherRoomPeerCount = len(view.Peers)
		facts.PeerConnectionTimedOut = s.updatePeerWait(status.Peers, view.Peers, facts.OtherRoomPeerCount)
		view.Session = s.machine.Apply(facts)
		logger.Infof("express view: localIP=%q mgmt=%v signal=%v daemonPeers=%d roomMembers=%d otherMembers=%d excluded=%d state=%s",
			status.LocalNetBirdIP,
			status.ManagementConnected,
			status.SignalConnected,
			len(status.Peers),
			len(peerSnapshot.Peers),
			facts.OtherRoomPeerCount,
			len(peerSnapshot.Peers)-len(view.Peers),
			view.Session.State,
		)
	}
	return view, nil
}

// excludeLocalPeer 从房间成员列表中剔除本机(以虚拟 IP 匹配)。
// Room API 返回的房间成员包含创建者自己,前端按"其他成员 + 1"渲染总人数,
// 这里过滤后成员列表只保留其他玩家。
// 注意 daemon 报告的本机 IP 带 CIDR 前缀(如 100.66.172.6/16),比较前需归一化。
func excludeLocalPeer(members []roomapi.Peer, localIP string) []roomapi.Peer {
	normalized := ipHost(localIP)
	if normalized == "" {
		return members
	}
	filtered := make([]roomapi.Peer, 0, len(members))
	for _, member := range members {
		if ipHost(member.NetBirdIP) != normalized {
			filtered = append(filtered, member)
		}
	}
	return filtered
}

// ipHost 去掉 IP 的 CIDR 前缀,只保留主机地址部分。
func ipHost(value string) string {
	if index := strings.IndexByte(value, '/'); index >= 0 {
		return value[:index]
	}
	return value
}

// viewWhileBusy 返回命令事务进行中的轻量快照,避免轮询线程
// 观察到"写了一半"的持久化状态并误报损坏。
func (s *Service) viewWhileBusy() RoomViewSnapshot {
	view := RoomViewSnapshot{
		Session:     s.machine.Snapshot(),
		Peers:       []roomapi.Peer{},
		DaemonPeers: []clientnetbird.Peer{},
		PeersStale:  true,
	}
	if metadata, err := s.metadata.Load(); err == nil {
		view.Metadata = metadata
	}
	if code, err := s.codes.Load(); err == nil {
		view.RoomCodeMasked = maskRoomCode(code)
		clearBytes(code)
	}
	if s.peers != nil {
		snapshot := s.peers.Snapshot()
		view.Peers = snapshot.Peers
		view.LastPeerRefresh = snapshot.LastRefreshAt
	}
	return view
}

// updatePeerWait 跟踪"房间成员已出现但隧道尚未建立"的时长。
// 超过 peerWaitTimeout 后返回超时事实,使状态机从
// StateConnectingPeer 降级为 StateReconnecting。
func (s *Service) updatePeerWait(daemonPeers []clientnetbird.Peer, roomMembers []roomapi.Peer, memberCount int) bool {
	roomIPs := make(map[string]struct{})
	for _, member := range roomMembers {
		roomIPs[ipHost(member.NetBirdIP)] = struct{}{}
	}
	progressing := false
	for _, peer := range daemonPeers {
		if _, ok := roomIPs[ipHost(peer.NetBirdIP)]; ok {
			progressing = true
			break
		}
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if progressing || memberCount <= 0 {
		s.peerWaitStart = time.Time{}
		return false
	}
	if s.peerWaitStart.IsZero() {
		s.peerWaitStart = now
		return false
	}
	return now.Sub(s.peerWaitStart) > peerWaitTimeout
}

func (s *Service) RevealRoomCode(context.Context) (string, error) {
	if _, err := s.loadSavedRoom(); err != nil {
		logger.Warnf("express reveal: saved room unavailable: %v", err)
		return "", err
	}
	code, err := s.codes.Load()
	if err != nil {
		logger.Warnf("express reveal: room code load failed: %v", err)
		return "", ErrStoredStateConflict
	}
	defer clearBytes(code)
	if len(code) == 0 {
		logger.Warnf("express reveal: room code is empty")
		return "", ErrStoredStateConflict
	}
	return string(code), nil
}

func (s *Service) Disconnect(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	metadata, err := s.loadSavedRoom()
	if err != nil {
		return s.failValidation(err)
	}
	if err := s.netbird.Disconnect(ctx, metadata.ProfileID); err != nil {
		return s.fail(err)
	}
	s.mu.Lock()
	s.disconnected = true
	s.mu.Unlock()
	return s.machine.Apply(Facts{RoomSaved: true, UserDisconnected: true}), nil
}

// Connect resumes the saved managed profile without requesting a new Setup Key.
func (s *Service) Connect(ctx context.Context) (Snapshot, error) {
	return s.Reconnect(ctx)
}

func (s *Service) Reconnect(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	s.mu.Lock()
	s.resumePending = false
	s.mu.Unlock()
	metadata, err := s.loadSavedRoom()
	if err != nil {
		return s.failValidation(err)
	}
	s.mu.Lock()
	s.disconnected = false
	s.peerWaitStart = time.Time{}
	s.mu.Unlock()
	s.machine.Apply(Facts{RoomSaved: true, ReconnectInProgress: true})
	status, err := s.monitor.Resume(ctx, metadata.ProfileID)
	if err != nil {
		return s.fail(err)
	}
	// 房主重连回房间:恢复心跳,防止被看门狗误判离线
	if s.isOwner() {
		s.startOwnerHeartbeat()
	}
	facts := Facts{
		RoomSaved:         true,
		ControlPlaneReady: status.ManagementConnected && status.SignalConnected,
		DaemonPeers:       status.Peers,
	}
	return s.machine.Apply(facts), nil
}

func (s *Service) Leave(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	return s.leaveUnlocked(ctx)
}

// ForceLeave 在本地存储不一致或 NetBird profile 无法验证时,
// 强制清理本地状态并回到 NoRoom(远端 profile 尽力清理,失败不阻塞恢复)。
func (s *Service) ForceLeave(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	return s.forceClear(ctx)
}

func (s *Service) State() Snapshot { return s.machine.Snapshot() }

func (s *Service) Create(ctx context.Context, hostname string) (Snapshot, error) {
	intent, err := roomapi.NewCreateIntent()
	if err != nil {
		return s.fail(err)
	}
	return s.enroll(ctx, hostname, func(ctx context.Context) (roomapi.Enrollment, error) {
		return s.rooms.Create(ctx, intent)
	})
}

type SwitchRequest struct {
	Mode      string
	RoomCode  string
	Hostname  string
	Confirmed bool
}

func (s *Service) Switch(ctx context.Context, request SwitchRequest) (Snapshot, error) {
	if !request.Confirmed {
		return s.State(), ErrSwitchConfirmationRequired
	}
	if request.Mode != "create" && request.Mode != "join" {
		return s.failValidation(ErrInvalidSwitchMode)
	}
	request.Hostname = strings.TrimSpace(request.Hostname)
	if request.Hostname == "" || utf8.RuneCountInString(request.Hostname) > 63 {
		return s.failValidation(errors.New("device name must contain 1 to 63 characters"))
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	if _, err := s.loadSavedRoom(); err == nil {
		if _, err := s.leaveUnlocked(ctx); err != nil {
			return s.State(), err
		}
	} else if !errors.Is(err, securestore.ErrNoRoomMetadata) {
		return s.failValidation(err)
	}

	if request.Mode == "create" {
		intent, err := roomapi.NewCreateIntent()
		if err != nil {
			return s.fail(err)
		}
		return s.enrollUnlocked(ctx, request.Hostname, func(ctx context.Context) (roomapi.Enrollment, error) {
			return s.rooms.Create(ctx, intent)
		})
	}
	return s.enrollUnlocked(ctx, request.Hostname, func(ctx context.Context) (roomapi.Enrollment, error) {
		return s.rooms.Join(ctx, request.RoomCode)
	})
}

func (s *Service) Join(ctx context.Context, roomCode, hostname string) (Snapshot, error) {
	return s.enroll(ctx, hostname, func(ctx context.Context) (roomapi.Enrollment, error) {
		return s.rooms.Join(ctx, roomCode)
	})
}

func (s *Service) enroll(ctx context.Context, hostname string, obtain func(context.Context) (roomapi.Enrollment, error)) (Snapshot, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" || utf8.RuneCountInString(hostname) > 63 {
		return s.failValidation(errors.New("device name must contain 1 to 63 characters"))
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := s.beginCommand(); err != nil {
		return s.State(), err
	}
	defer s.endCommand()
	return s.enrollUnlocked(ctx, hostname, obtain)
}

func (s *Service) enrollUnlocked(ctx context.Context, hostname string, obtain func(context.Context) (roomapi.Enrollment, error)) (Snapshot, error) {
	if err := s.requireEmptyStorage(); err != nil {
		// 用户在待恢复状态下显式创建/加入新房:视为放弃上次的房,
		// 先强制清理旧记录与远端 profile,再继续新房间流程。
		// 过期房间同样在 requireEmptyStorage 中已清除并放行。
		s.mu.Lock()
		pending := s.resumePending
		s.mu.Unlock()
		if pending {
			if _, clearErr := s.forceClear(ctx); clearErr != nil {
				return s.fail(clearErr)
			}
		} else {
			return s.failValidation(err)
		}
	}
	return s.performEnrollment(ctx, hostname, obtain, false)
}

// performEnrollment 执行"获取房间凭证 → 重置 daemon 身份 → 注册并进房"的完整事务。
// 从 enrollUnlocked(前端命令)与 repairRoomIdentity(自愈)两个入口共用。
// preserveLocalState 供 repair 路径置 true：补偿时保留本地房间记录
// （codes/metadata/tokens 属于用户当前房间，re-join 失败不得清除）。
func (s *Service) performEnrollment(ctx context.Context, hostname string, obtain func(context.Context) (roomapi.Enrollment, error), preserveLocalState bool) (Snapshot, error) {
	s.machine.Apply(Facts{EnrollmentInProgress: true})
	logger.Infof("express enroll: requesting room (hostname=%q)", hostname)

	enrollment, err := obtain(ctx)
	if err != nil {
		logger.Errorf("express enroll: room create/join failed: %v", err)
		return s.fail(err)
	}
	defer enrollment.DiscardSetupKey()
	// NetBird setup key 的 auto_groups 只在 peer 首次注册时生效;
	// 重复进房必须废弃旧身份,否则 peer 留在旧房间组,成员互相不可见。
	s.resetManagedIdentity(ctx)
	profile, err := s.netbird.CreateProfile(ctx, clientnetbird.ManagedProfileName)
	if err != nil {
		logger.Errorf("express enroll: create managed profile failed: %v", err)
		return s.fail(&TransactionError{Cause: err})
	}
	transaction := enrollmentTransaction{
		service:            s,
		profileID:          profile.ID,
		preserveLocalState: preserveLocalState,
	}
	if profile.ID == "" {
		return s.fail(&TransactionError{Cause: clientnetbird.ErrManagedProfileInconsistent})
	}
	if profile.Name != clientnetbird.ManagedProfileName {
		return s.fail(transaction.wrap(clientnetbird.ErrManagedProfileInconsistent, ctx))
	}
	committed := false
	defer func() {
		if !committed {
			transaction.compensate(ctx)
		}
	}()

	transaction.enrollmentAttempted = true
	err = enrollment.ConsumeSetupKey(func(key *clientnetbird.SetupKey) error {
		return s.netbird.Enroll(ctx, clientnetbird.EnrollmentRequest{
			ManagementURL: enrollment.ManagementURL,
			ProfileID:     profile.ID,
			Hostname:      hostname,
			SetupKey:      key,
		})
	})
	if err != nil {
		logger.Errorf("express enroll: daemon enroll failed: %v", err)
		return s.fail(transaction.wrap(err, ctx))
	}
	if err := s.netbird.Connect(ctx, profile.ID); err != nil {
		logger.Errorf("express enroll: daemon connect failed: %v", err)
		return s.fail(transaction.wrap(err, ctx))
	}

	roomCode := []byte(enrollment.RoomCode)
	defer clearBytes(roomCode)
	transaction.codeWriteAttempted = true
	if err := s.codes.Save(roomCode); err != nil {
		return s.fail(transaction.wrap(err, ctx))
	}
	transaction.metadataWriteAttempted = true
	if err := s.metadata.Save(securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        enrollment.RoomID,
		ManagementURL: enrollment.ManagementURL,
		ProfileID:     profile.ID,
		CreatedAt:     s.now().UTC(),
		RelayEnabled:  enrollment.RelayEnabled,
	}); err != nil {
		return s.fail(transaction.wrap(err, ctx))
	}
	// 房主令牌(仅 Create 响应携带):与房间码同级保护落盘,支撑解散/心跳
	transaction.ownerTokenWriteAttempted = true
	if enrollment.OwnerToken != "" && s.tokens != nil {
		ownerToken := []byte(enrollment.OwnerToken)
		err := s.tokens.Save(ownerToken)
		clearBytes(ownerToken)
		if err != nil {
			return s.fail(transaction.wrap(err, ctx))
		}
	}
	committed = true

	// 房主:创建成功起心跳,防服务端看门狗误判离线
	if enrollment.OwnerToken != "" && s.tokens != nil {
		s.startOwnerHeartbeat()
	}

	s.mu.Lock()
	s.disconnected = false
	s.peerWaitStart = time.Time{}
	s.repairPolls = 0
	s.mu.Unlock()
	facts := Facts{RoomSaved: true}
	if status, statusErr := s.netbird.Status(ctx); statusErr == nil {
		facts.ControlPlaneReady = status.ManagementConnected && status.SignalConnected
		facts.DaemonPeers = status.Peers
		logger.Infof("express enroll: room ready (controlPlane=%v daemonPeers=%d)", facts.ControlPlaneReady, len(status.Peers))
	}
	return s.machine.Apply(facts), nil
}

func (s *Service) leaveUnlocked(ctx context.Context) (Snapshot, error) {
	metadata, err := s.loadSavedRoom()
	if err != nil {
		if errors.Is(err, securestore.ErrNoRoomMetadata) {
			return s.machine.Apply(Facts{}), nil
		}
		// 存储冲突等情形:走强制清理路径,不因远端失败阻塞恢复。
		return s.forceClear(ctx)
	}
	s.stopOwnerHeartbeat()
	// 房主:先在服务端解散房间(成员由此掉线),再走本地断开流程。
	s.closeRoomIfOwner(ctx)
	if err := s.netbird.Deregister(ctx, metadata.ProfileID); err != nil && !profileAlreadyGone(err) {
		return s.fail(err)
	}
	if err := s.netbird.RemoveProfile(ctx, metadata.ProfileID); err != nil && !profileAlreadyGone(err) {
		return s.fail(err)
	}
	clearFailures := s.clearLocalRoom()
	if clearFailures > 0 {
		return s.fail(&TransactionError{CleanupFailures: clearFailures})
	}
	return s.machine.Apply(Facts{}), nil
}

// clearLocalRoom 清空本地保存的房间记录并复位会话辅助状态,
// 返回清理失败的操作数。
func (s *Service) clearLocalRoom() int {
	var clearFailures int
	if err := s.metadata.Clear(); err != nil {
		clearFailures++
	}
	if err := s.codes.Clear(); err != nil {
		clearFailures++
	}
	s.mu.Lock()
	tokens := s.tokens
	s.disconnected = false
	s.peerWaitStart = time.Time{}
	s.resumePending = false
	s.lastLocalIP = ""
	s.repairPolls = 0
	s.mu.Unlock()
	if tokens != nil {
		if err := tokens.Clear(); err != nil {
			clearFailures++
		}
	}
	return clearFailures
}

// forceClear 在本地存储不一致或 NetBird profile 无法验证时强制清理:
// 尽力移除远端 profile(失败不阻塞),本地文件清理失败则报错。
func (s *Service) forceClear(ctx context.Context) (Snapshot, error) {
	s.stopOwnerHeartbeat()
	if metadata, err := s.metadata.Load(); err == nil && metadata.ProfileID != "" {
		if expiredRoom(metadata, s.now()) {
			logger.Infof("express force-clear: room expired, skipping remote cleanup")
		} else {
			_ = s.netbird.Deregister(ctx, metadata.ProfileID)
			_ = s.netbird.RemoveProfile(ctx, metadata.ProfileID)
		}
	}
	clearFailures := s.clearLocalRoom()
	if clearFailures > 0 {
		return s.fail(&TransactionError{CleanupFailures: clearFailures})
	}
	return s.machine.Apply(Facts{}), nil
}

func (s *Service) beginCommand() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrCommandInProgress
	}
	s.busy = true
	return nil
}

func (s *Service) endCommand() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *Service) requireEmptyStorage() error {
	metadata, metadataErr := s.metadata.Load()
	code, codeErr := s.codes.Load()
	clearBytes(code)
	metadataMissing := errors.Is(metadataErr, securestore.ErrNoRoomMetadata)
	codeMissing := errors.Is(codeErr, securestore.ErrNoProtectedRoomCode)
	if metadataErr == nil && codeErr == nil {
		// 过期房间视为失效:创建/加入新房间前清除,避免残留陈旧记录。
		if expiredRoom(metadata, s.now()) {
			_ = s.metadata.Clear()
			_ = s.codes.Clear()
			return nil
		}
		return ErrRoomAlreadySaved
	}
	if metadataMissing && codeMissing {
		return nil
	}
	return ErrStoredStateConflict
}

func (s *Service) loadSavedRoom() (securestore.RoomMetadata, error) {
	metadata, metadataErr := s.metadata.Load()
	code, codeErr := s.codes.Load()
	clearBytes(code)
	if metadataErr == nil && codeErr == nil {
		if expiredRoom(metadata, s.now()) {
			return securestore.RoomMetadata{}, ErrRoomExpired
		}
		return metadata, nil
	}
	if errors.Is(metadataErr, securestore.ErrNoRoomMetadata) && errors.Is(codeErr, securestore.ErrNoProtectedRoomCode) {
		return securestore.RoomMetadata{}, securestore.ErrNoRoomMetadata
	}
	return securestore.RoomMetadata{}, ErrStoredStateConflict
}

// expiredRoom 判断保存的房间是否已超过有效期。
func expiredRoom(metadata securestore.RoomMetadata, now time.Time) bool {
	return now.Sub(metadata.CreatedAt) > roomMaxAge
}

// SetResumePending 标记本地是否保存了"待确认恢复"的上次房间。
// 仅在应用启动时调用;若保存的房间已过期或不存在,则不会进入待恢复状态。
func (s *Service) SetResumePending(pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pending {
		if _, err := s.loadSavedRoom(); err == nil {
			s.resumePending = true
		}
		return
	}
	s.resumePending = false
}

// closeRoomIfOwner 房主离开房间前先解散服务端房间。尽力而为：
// 网络故障也不阻塞本地离开（服务端看门狗会在 OwnerOfflineAfter 后兜底）。
func (s *Service) closeRoomIfOwner(ctx context.Context) {
	s.mu.Lock()
	tokens := s.tokens
	ownerAPI := s.ownerAPI
	s.mu.Unlock()
	if tokens == nil || ownerAPI == nil {
		return
	}
	token, err := tokens.Load()
	if err != nil || len(token) == 0 {
		return // 普通成员或旧数据,无房主令牌
	}
	defer clearBytes(token)
	code, err := s.codes.Load()
	if err != nil {
		return
	}
	defer clearBytes(code)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := ownerAPI.CloseRoom(closeCtx, string(code), string(token)); err != nil {
		logger.Warnf("express leave: close room as owner failed (本地离开仍继续): %v", err)
	} else {
		logger.Infof("express leave: room closed by owner")
	}
}

// startOwnerHeartbeat 房主心跳：周期性向服务端证明房主在线,
// 防止被"房主离线自动解散"的看门狗误回收。调用会重启已在跑的心跳。
// WithCancel 与 heartbeatCancel 赋值必须在同一把锁内完成,否则并发调用
// 会让先注册的 cancel 被覆盖、旧 goroutine 失去取消句柄而泄漏。
func (s *Service) startOwnerHeartbeat() {
	s.mu.Lock()
	tokens := s.tokens
	ownerAPI := s.ownerAPI
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.heartbeatCancel != nil {
		s.heartbeatCancel()
		s.heartbeatCancel = nil
	}
	if tokens == nil || ownerAPI == nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.heartbeatCancel = cancel
	s.mu.Unlock()
	go func() {
		// 首跳立即发出:让服务端尽早记录 last_owner_heartbeat,
		// 这样看门狗(只清扫"有过心跳且超时"的房间)才能对新建房间生效;
		// 否则房主在首个 60s tick 之前崩溃,房间会因从未心跳而永不被回收。
		delay := time.Duration(0)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				token, err := tokens.Load()
				if err != nil {
					return // 令牌被清(已离开):停止心跳
				}
				code, codeErr := s.codes.Load()
				if codeErr != nil {
					clearBytes(token)
					return
				}
				hbCtx, hbCancel := context.WithTimeout(ctx, 10*time.Second)
				err = ownerAPI.Heartbeat(hbCtx, string(code), string(token))
				hbCancel()
				clearBytes(token)
				clearBytes(code)
				if err != nil {
					var httpErr *roomapi.HTTPError
					if errors.As(err, &httpErr) {
						if httpErr.Code == roomapi.ErrorRoomClosed || httpErr.StatusCode == http.StatusForbidden {
							// 房间已被解散(410)或房主令牌失效(403):停止心跳,
							// 房间已解散由 View 轮询做本地收尾;令牌失效则继续跳也只会 403。
							return
						}
					}
					// 失败指数退避(60s→120s→120s…,封顶 2 分钟),成功复位;
				// 封顶值必须恒小于服务端房主离线回收阈值(默认 5m)。
					if delay <= 0 {
						delay = ownerHeartbeatInterval
					} else if delay < ownerHeartbeatMaxBackoff {
						delay *= 2
						if delay > ownerHeartbeatMaxBackoff {
							delay = ownerHeartbeatMaxBackoff
						}
					}
					logger.Warnf("express owner heartbeat failed (backoff %v): %v", delay, err)
				} else {
					delay = ownerHeartbeatInterval
				}
				timer.Reset(delay)
			}
		}
	}()
}

func (s *Service) stopOwnerHeartbeat() {
	s.mu.Lock()
	cancel := s.heartbeatCancel
	s.heartbeatCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close 停止该实例的全部后台 goroutine（房主心跳）并阻止新的心跳/修复
// 启动，使实例可被 GC。ExpressController 在 MSI 修复后重组装、整体替换
// Service 实例前必须调用旧实例的 Close——否则旧心跳 goroutine 在令牌
// 未清除时没有任何停止通道，会造成新旧实例对同一房间双心跳并发、
// 旧实例整棵对象图（含已关闭的 RPC adapter）常驻内存。
// 进行中的身份自愈 goroutine 有独立的超时预算（2×commandTimeout），
// 会自行退出，Close 不阻塞等待。
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopOwnerHeartbeat()
}

// profileAlreadyGone 判断错误是否表示"远端/守护进程中的 profile 已不存在"。
// 残留房间记录对应的 profile 被删除后,Leave 应视为已清理并继续本地清理,
// 避免用户永远无法离开一个"远端已不存在"的房间。
func profileAlreadyGone(err error) bool {
	return errors.Is(err, clientnetbird.ErrManagedProfileInconsistent) ||
		errors.Is(err, clientnetbird.ErrManagedProfileConflict)
}

// fail 用于运行时/远端失败:状态机进入 RecoverableError,用户可重试。
func (s *Service) fail(err error) (Snapshot, error) {
	return s.machine.Apply(Facts{RecoverableError: true}), err
}

// failValidation 用于输入校验与本地状态冲突错误:返回错误但
// 不改变会话状态,避免把正常场景(如已保存房间)误判为故障。
func (s *Service) failValidation(err error) (Snapshot, error) {
	return s.machine.Snapshot(), err
}

type enrollmentTransaction struct {
	service                  *Service
	profileID                string
	enrollmentAttempted      bool
	codeWriteAttempted       bool
	metadataWriteAttempted   bool
	ownerTokenWriteAttempted bool
	// preserveLocalState 为 true 时补偿不清除本地存储（codes/metadata/
	// tokens）：身份自愈路径（repair）复用本事务，但其本地记录属于
	// 用户当前房间——re-join 失败清除它们会让用户"自愈反而丢房间"
	// （UI 卡死在无记录态、房主心跳停 → 服务端 5 分钟回收）。
	// daemon 侧回滚（Deregister/RemoveProfile）不受影响。
	preserveLocalState bool
	compensated         bool
	cleanupFailures     int
}

// compensate 按写入的严格逆序清理（tokens → metadata → codes，
// 远端 profile 最先回滚），与"codes → metadata → tokens"的写入序对应。
// preserveLocalState 路径跳过本地三类清理。
func (t *enrollmentTransaction) compensate(parent context.Context) {
	if t.compensated {
		return
	}
	t.compensated = true
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
	defer cancel()
	if t.enrollmentAttempted {
		if err := t.service.netbird.Deregister(ctx, t.profileID); err != nil {
			t.cleanupFailures++
		}
	}
	if err := t.service.netbird.RemoveProfile(ctx, t.profileID); err != nil {
		t.cleanupFailures++
	}
	if t.preserveLocalState {
		return
	}
	if t.ownerTokenWriteAttempted && t.service.tokens != nil {
		if err := t.service.tokens.Clear(); err != nil {
			t.cleanupFailures++
		}
	}
	if t.metadataWriteAttempted {
		if err := t.service.metadata.Clear(); err != nil {
			t.cleanupFailures++
		}
	}
	if t.codeWriteAttempted {
		if err := t.service.codes.Clear(); err != nil {
			t.cleanupFailures++
		}
	}
}

func (t *enrollmentTransaction) wrap(cause error, parent context.Context) error {
	t.compensate(parent)
	return &TransactionError{Cause: cause, CleanupFailures: t.cleanupFailures}
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func maskRoomCode(value []byte) string {
	if len(value) < 4 {
		return ""
	}
	masked := make([]byte, len(value))
	copy(masked, value)
	for index := 0; index < len(masked)-4; index++ {
		if masked[index] != '-' {
			masked[index] = '*'
		}
	}
	return string(masked)
}
