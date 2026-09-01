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
	// lastLocalIP 记录最近一次观察到(或注册)的本机虚拟 IP。
	// daemon 断开时 LocalNetBirdIP 为空,用它仍能把房间成员中的
	// 自己排除,避免断开后自己被打成"其他成员"导致人数 +1。
	lastLocalIP string
}

func NewService(rooms RoomAPI, netbird clientnetbird.Adapter, metadata MetadataStorage, codes RoomCodeStorage) *Service {
	var peers *PeerRefresher
	if api, ok := rooms.(PeerAPI); ok {
		peers = NewPeerRefresher(api, codes)
	}
	return &Service{
		rooms:    rooms,
		netbird:  netbird,
		metadata: metadata,
		codes:    codes,
		machine:  NewMachine(),
		monitor:  clientnetbird.NewRecoveryMonitor(netbird),
		peers:    peers,
		now:      time.Now,
	}
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
	}
	if s.peers != nil {
		peerSnapshot, _ := s.peers.Refresh(ctx)
		localIP := status.LocalNetBirdIP
		if ipHost(localIP) == "" {
			s.mu.Lock()
			localIP = s.lastLocalIP
			s.mu.Unlock()
		}
		view.Peers = excludeLocalPeer(peerSnapshot.Peers, localIP)
		view.PeersStale = peerSnapshot.Stale
		view.LastPeerRefresh = peerSnapshot.LastRefreshAt
		facts.OtherRoomPeerCount = countDistinctPeers(status.Peers, view.Peers, localIP)
		facts.PeerConnectionTimedOut = s.updatePeerWait(status.Peers, facts.OtherRoomPeerCount)
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
func (s *Service) updatePeerWait(daemonPeers []clientnetbird.Peer, memberCount int) bool {
	progressing := len(daemonPeers) > 0
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

// countDistinctPeers 合并守护进程对端与房间成员(以虚拟 IP 去重),
// 用于派生"是否正在等待对等体"的状态。本机虚拟 IP 会被排除,
// 否则单人房间会把"自己"误判为等待连接的其他成员。
// IP 比较前会去掉 CIDR 前缀,因为 daemon 报告的 IP 形如 100.66.172.6/16。
func countDistinctPeers(daemon []clientnetbird.Peer, members []roomapi.Peer, localIP string) int {
	normalizedLocal := ipHost(localIP)
	seen := make(map[string]struct{})
	for _, peer := range daemon {
		key := ipHost(peer.NetBirdIP)
		if key == "" {
			key = "d:" + peer.FQDN
		}
		if key != "" && key != "d:" && key != normalizedLocal {
			seen[key] = struct{}{}
		}
	}
	for _, peer := range members {
		key := ipHost(peer.NetBirdIP)
		if key == "" {
			key = "m:" + peer.ID
		}
		if key != "" && key != "m:" && key != normalizedLocal {
			seen[key] = struct{}{}
		}
	}
	return len(seen)
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
		// 用户在待恢复状态下显式创建/加入新房间:视为放弃上次的房间,
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
	s.machine.Apply(Facts{EnrollmentInProgress: true})
	logger.Infof("express enroll: requesting room (hostname=%q)", hostname)

	enrollment, err := obtain(ctx)
	if err != nil {
		logger.Errorf("express enroll: room create/join failed: %v", err)
		return s.fail(err)
	}
	defer enrollment.DiscardSetupKey()
	profile, err := s.netbird.CreateProfile(ctx, clientnetbird.ManagedProfileName)
	if err != nil {
		logger.Errorf("express enroll: create managed profile failed: %v", err)
		return s.fail(&TransactionError{Cause: err})
	}
	transaction := enrollmentTransaction{
		service:   s,
		profileID: profile.ID,
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
	committed = true

	s.mu.Lock()
	s.disconnected = false
	s.peerWaitStart = time.Time{}
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
		// 存储冲突(孤儿状态):走强制清理路径,不因远端失败阻塞恢复。
		return s.forceClear(ctx)
	}
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
	s.disconnected = false
	s.peerWaitStart = time.Time{}
	s.resumePending = false
	s.lastLocalIP = ""
	s.mu.Unlock()
	return clearFailures
}

// forceClear 在本地存储不一致或 NetBird profile 无法验证时强制清理:
// 尽力移除远端 profile(失败不阻塞),本地文件清理失败则报错。
func (s *Service) forceClear(ctx context.Context) (Snapshot, error) {
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
	service                *Service
	profileID              string
	enrollmentAttempted    bool
	codeWriteAttempted     bool
	metadataWriteAttempted bool
	compensated            bool
	cleanupFailures        int
}

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
