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

package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sogame/internal/logger"
	clientnetbird "sogame/internal/netbird"
	"sogame/internal/nbdaemon"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
	"sogame/internal/session"
)

// ExpressState 是极速模式暴露给前端的完整状态快照。
// 所有敏感字段（房间码、IP、profileId）在序列化时会被脱敏，
// 完整房间码通过 ExpressRevealRoomCode 单独获取。
type ExpressState struct {
	State          string         `json:"state"`          // NoRoom/Enrolling/ConnectedP2P/ConnectedRelay/RecoverableError 等
	RoomID         string         `json:"roomId"`         // 脱敏后的房间 ID
	RoomCodeMasked string         `json:"roomCodeMasked"` // 脱敏后的房间码（供用户辨认）
	LocalIP        string         `json:"localIp"`        // 本机 NetBird 虚拟 IP
	ConnectedPath  string         `json:"connectedPath"`  // none / p2p / relay
	Peers          []ExpressPeer  `json:"peers"`          // 房间内其他成员
	PeersStale     bool           `json:"peersStale"`     // 对等体列表是否可能过期
	Service        ExpressService `json:"service"`        // NetBird 守护进程状态
	Error          *ExpressError  `json:"error"`          // 最近一次错误（nil 表示无错误）
	BusyCommand    string         `json:"busyCommand"`    // 正在执行的命令（空表示空闲）
	HasSavedRoom   bool           `json:"hasSavedRoom"`   // 本地保存了上次的房间且等待用户确认恢复
	// Disconnected 用户已主动断开（可重新连接）。
	Disconnected   bool           `json:"disconnected"`
	// DaemonPreparing 守护进程自动就绪流程进行中（准备中提示由前端展示）。
	DaemonPreparing bool          `json:"daemonPreparing"`
	// RoomCode 完整房间码。状态快照中始终为空串：完整码是入会凭证，
	// 仅经 ExpressRevealRoomCode 按需获取，不随轮询/事件广播下发。
	RoomCode string `json:"roomCode"`
	// RelayEnabled 该房间所属服务器是否允许 Relay 中继（由服务器下发，客户端被动遵循）。
	RelayEnabled bool `json:"relayEnabled"`
	// RelayBlocked 服务器不允许 Relay，但底层 daemon 检测到中继连接（被忽略）：
	// 用于前端提示"该服务器已关闭中继，无法建立 P2P 直连"。
	RelayBlocked bool `json:"relayBlocked"`
	// IsOwner 本机是当前房间的创建者(房主)：UI 据此把"离开"换成"解散房间"。
	IsOwner bool `json:"isOwner"`
}

// ExpressPeer 是房间内的一个成员。
type ExpressPeer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NetBirdIP string `json:"netbirdIp"`
	Connected bool   `json:"connected"`
	Path      string `json:"path"` // none / p2p / relay
}

// ExpressService 描述 NetBird 守护进程的安装与运行状态。
type ExpressService struct {
	Installed       bool   `json:"installed"`
	Running         bool   `json:"running"`
	Version         string `json:"version"`
	ExpectedVersion string `json:"expectedVersion"`
	RepairRequired  bool   `json:"repairRequired"`
	UpdateAvailable bool   `json:"updateAvailable"`
	// DaemonNeedsSetup 守护进程需要自动补齐（未安装/版本不匹配/未运行）。
	// 前端据此显示"待准备"提示条；用户点击或执行房间命令时自动补齐。
	DaemonNeedsSetup bool `json:"daemonNeedsSetup"`
}

// ExpressError 是面向用户的错误信息。
type ExpressError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action,omitempty"`
}

func (e *ExpressError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code
}

// 错误码常量
const (
	expressErrInvalidInput       = "INVALID_INPUT"
	expressErrRoomUnavailable    = "ROOM_UNAVAILABLE"
	expressErrRoomClosed         = "ROOM_CLOSED" // 房主解散或离线被回收
	expressErrRoomAPIRateLimited = "ROOM_API_RATE_LIMITED"
	expressErrRoomAPIUnavailable = "ROOM_API_UNAVAILABLE"
	expressErrServiceMissing     = "NETBIRD_SERVICE_MISSING"
	expressErrServiceUnavailable = "NETBIRD_SERVICE_UNAVAILABLE"
	expressErrVersionMismatch    = "NETBIRD_VERSION_MISMATCH"
	expressErrProfileConflict    = "NETBIRD_PROFILE_CONFLICT"
	expressErrEnrollmentFailed   = "ENROLLMENT_FAILED"
	expressErrOperationConflict  = "OPERATION_CONFLICT"
	expressErrSecureStore        = "SECURE_STORE_UNAVAILABLE"
	expressErrInternal           = "INTERNAL"
)

// ExpressController 是极速模式的协调器，封装 Room API + NetBird 守护进程的
// 房间生命周期管理。它将 sogame-netbird 的 session.Service 适配为 Wails 可绑定的接口。
type ExpressController struct {
	mu      sync.Mutex
	ctx     context.Context
	rooms   *session.Service
	close   func() error
	service *nbdaemon.ServiceInspector
	repair  func(context.Context) error
	state   ExpressState
	// setup 守护进程自动就绪状态（独立锁：就绪流程长达数十秒，
	// 不能与高频状态读写共用 c.mu）。
	setup daemonSetupState
}

// daemonSetupState 守护进程自动就绪的协调状态。
type daemonSetupState struct {
	mu       sync.Mutex
	running  bool             // 一个就绪流程进行中
	done     chan struct{}    // 就绪流程完成信号（并发调用方等待复用结果）
	err      error            // 最近一次就绪结果
	failures int              // 连续失败次数（达到上限后停止自动重试，防 UAC 轰炸）
	lastAt   time.Time        // 最近一次失败时间（冷却判定）
}

const (
	// daemonSetupCooldown 自动就绪失败后的冷却期。
	daemonSetupCooldown = 60 * time.Second
	// daemonSetupMaxRetries 连续自动重试上限；超过后仅当用户执行新操作时重试。
	daemonSetupMaxRetries = 3
	// daemonSetupTimeout 单次就绪流程的总预算（msiexec 首装可能 30s+）。
	daemonSetupTimeout = 90 * time.Second
)

var (
	errDaemonSetupUnavailable = errors.New("daemon setup unavailable")
	errDaemonSetupGiveUp      = errors.New("daemon setup failed repeatedly")
	errDaemonSetupTimeout     = errors.New("daemon setup timed out")
)

// NewExpressController 创建一个空的极速模式协调器。
// 实际的 session/service 注入由平台特定的构造函数（NewWindowsExpressController）完成。
func NewExpressController() *ExpressController {
	return &ExpressController{
		state: ExpressState{
			State:         string(session.StateNoRoom),
			ConnectedPath: string(clientnetbird.PathNone),
			Peers:         []ExpressPeer{},
			Service: ExpressService{
				ExpectedVersion: clientnetbird.ExpectedVersion,
			},
		},
	}
}

// Configure 注入 session 服务、RPC 关闭函数、服务检查器和修复函数。
func (c *ExpressController) Configure(rooms *session.Service, closeFn func() error, inspector *nbdaemon.ServiceInspector, repair func(context.Context) error) {
	c.mu.Lock()
	c.rooms = rooms
	c.close = closeFn
	c.service = inspector
	c.repair = repair
	c.mu.Unlock()
}

// Startup 在 Wails 启动时调用，启动后台刷新协程。
func (c *ExpressController) Startup(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	rooms := c.rooms
	c.mu.Unlock()
	logger.Infof("express: controller started")
	// 启动时若本地保存了上次的房间,标记为"待用户确认恢复":
	// 在此之前 View 保持 NoRoom,界面不进入房间视图、不自动重连,
	// 只有用户显式点击"恢复"/"离开"才会改动房间状态。
	if rooms != nil {
		rooms.SetResumePending(true)
	}
	go c.refreshRoomView(ctx)
	go c.refreshService(ctx)
}

// Shutdown 在 Wails 关闭时调用，停止后台 goroutine 并释放 RPC 连接。
func (c *ExpressController) Shutdown(_ context.Context) {
	c.mu.Lock()
	closeFn := c.close
	rooms := c.rooms
	c.mu.Unlock()
	if rooms != nil {
		rooms.Close()
	}
	if closeFn != nil {
		if err := closeFn(); err != nil {
			logger.Warnf("express: close NetBird RPC adapter: %v", err)
		}
	}
	logger.Infof("express: controller stopped")
}

// GetState 返回极速模式的当前状态快照。
func (c *ExpressController) GetState() ExpressState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cloneState()
}

// CreateRoom 创建一个新房间。返回更新后的状态。
func (c *ExpressController) CreateRoom(displayName string) ExpressState {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		hostname, _ := os.Hostname()
		displayName = hostname
	}
	if state, ok := c.ensureDaemonForCommand(); !ok {
		return state
	}
	return c.runRoomCommand("create", func(ctx context.Context) (session.Snapshot, error) {
		if c.rooms == nil {
			return session.Snapshot{}, errors.New("room session is unavailable")
		}
		return c.rooms.Create(ctx, displayName)
	})
}

// JoinRoom 加入一个已有房间。返回更新后的状态。
func (c *ExpressController) JoinRoom(roomCode, displayName string) ExpressState {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		hostname, _ := os.Hostname()
		displayName = hostname
	}
	roomCode = strings.TrimSpace(roomCode)
	if roomCode == "" {
		return c.failCommand(expressErrInvalidInput, "房间码不能为空", false, "请输入房间码")
	}
	if state, ok := c.ensureDaemonForCommand(); !ok {
		return state
	}
	return c.runRoomCommand("join", func(ctx context.Context) (session.Snapshot, error) {
		if c.rooms == nil {
			return session.Snapshot{}, errors.New("room session is unavailable")
		}
		return c.rooms.Join(ctx, roomCode, displayName)
	})
}

// Disconnect 断开当前房间的连接（保留房间记录，可重新连接）。
func (c *ExpressController) Disconnect() ExpressState {
	return c.runCommand("disconnect", func(ctx context.Context) (session.Snapshot, error) {
		if c.rooms == nil {
			return session.Snapshot{}, errors.New("room session is unavailable")
		}
		return c.rooms.Disconnect(ctx)
	})
}

// Reconnect 重新连接到已保存的房间。
func (c *ExpressController) Reconnect() ExpressState {
	if state, ok := c.ensureDaemonForCommand(); !ok {
		return state
	}
	return c.runCommand("connect", func(ctx context.Context) (session.Snapshot, error) {
		if c.rooms == nil {
			return session.Snapshot{}, errors.New("room session is unavailable")
		}
		return c.rooms.Connect(ctx)
	})
}

// LeaveRoom 离开房间，清除本地保存的房间数据。
func (c *ExpressController) LeaveRoom() ExpressState {
	return c.runCommand("leave", func(ctx context.Context) (session.Snapshot, error) {
		if c.rooms == nil {
			return session.Snapshot{}, errors.New("room session is unavailable")
		}
		return c.rooms.Leave(ctx)
	})
}

// RevealRoomCode 返回完整的房间码（供用户分享给朋友）。
// 房间命令进行中或当前不在房间时静默返回空码：房间码由命令完成后的
// 轮询刷新自动补充,避免把"创建/加入进行中"误报为用户可见的错误。
func (c *ExpressController) RevealRoomCode() (string, *ExpressError) {
	c.mu.Lock()
	rooms := c.rooms
	ctx := c.ctx
	busy := c.state.BusyCommand != ""
	inRoom := c.state.State != "" && c.state.State != string(session.StateNoRoom) && c.state.State != string(session.StateRecoverableError)
	c.mu.Unlock()
	if busy || !inRoom {
		logger.Debugf("express reveal: skipped (busy=%v inRoom=%v)", busy, inRoom)
		return "", nil
	}
	if rooms == nil {
		return "", &ExpressError{Code: expressErrServiceUnavailable, Message: "房间会话不可用", Retryable: true, Action: "检查服务后重试"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	code, err := rooms.RevealRoomCode(ctx)
	if err != nil {
		logger.Warnf("express reveal: failed: %v", err)
		return "", expressPublicError(err)
	}
	logger.Infof("express reveal: room code revealed")
	return code, nil
}

// ExpressPrepareDaemon 由前端"立即准备"提示条按钮调用：主动触发守护进程
// 自动就绪（含一次 UAC）。房间命令（创建/加入/重连）入口也会自动调用
// EnsureDaemonReady，无需用户感知守护进程概念。
// 守护进程是一体化内建依赖（docs/DAEMON_AUTO_SETUP_DESIGN.md）——
// UI 不再有"安装/修复守护进程"概念。
func (c *ExpressController) ExpressPrepareDaemon() ExpressState {
	return c.prepareDaemon()
}

// prepareDaemon 触发自动就绪并把结果映射为状态快照。
func (c *ExpressController) prepareDaemon() ExpressState {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.EnsureDaemonReady(ctx); err != nil {
		return c.failDaemonSetup(err)
	}
	c.refreshService(ctx)
	return c.GetState()
}

// EnsureDaemonReady 保证守护进程可用：不健康时自动修复（含一次 UAC），
// 成功后重组装控制器。与房间命令共用 BusyCommand 互斥（busy="daemon-setup"）。
// 并发调用复用同一就绪流程的结果；失败冷却 60s，连续 3 次失败后停止
// 自动重试（防止杀软拦截场景下的 UAC 弹窗轰炸）。
// 返回 nil 表示就绪；返回错误表示准备失败。
func (c *ExpressController) EnsureDaemonReady(ctx context.Context) error {
	// 快路径：已就绪直接返回（巡检由 refreshService 后台轮询负责）。
	if c.daemonHealthy(ctx) {
		c.setup.mu.Lock()
		c.setup.failures = 0
		c.setup.mu.Unlock()
		return nil
	}

	c.setup.mu.Lock()
	if c.setup.running {
		// 已有就绪流程在跑：等待其完成并复用结果。
		done := c.setup.done
		c.setup.mu.Unlock()
		select {
		case <-done:
			c.setup.mu.Lock()
			err := c.setup.err
			c.setup.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.setup.failures >= daemonSetupMaxRetries {
		err := errDaemonSetupGiveUp
		c.setup.mu.Unlock()
		c.setDaemonError(err)
		return err
	}
	if c.setup.err != nil && !c.setup.lastAt.IsZero() && time.Since(c.setup.lastAt) < daemonSetupCooldown {
		// 冷却期内：直接返回上次错误，不重复弹 UAC。
		err := c.setup.err
		c.setup.mu.Unlock()
		return err
	}
	// 占用本次就绪流程。
	c.setup.running = true
	c.setup.done = make(chan struct{})
	c.setup.mu.Unlock()

	err := c.performDaemonSetup(ctx)

	c.setup.mu.Lock()
	c.setup.running = false
	c.setup.err = err
	if err != nil {
		c.setup.failures++
		c.setup.lastAt = time.Now()
	} else {
		c.setup.failures = 0
		c.setup.lastAt = time.Time{}
		c.setup.err = nil
	}
	close(c.setup.done)
	c.setup.mu.Unlock()
	if err != nil {
		c.setDaemonError(err)
	}
	return err
}

// performDaemonSetup 执行一次就绪流程：占用 daemon-setup busy 位、
// 调用修复函数（UAC + msiexec）、等待服务就绪。
func (c *ExpressController) performDaemonSetup(ctx context.Context) error {
	c.mu.Lock()
	repair := c.repair
	c.state.BusyCommand = "daemon-setup"
	c.state.Error = nil
	state := c.cloneState()
	c.mu.Unlock()
	if c.ctx != nil {
		runtime.EventsEmit(c.ctx, "express:state-changed", state)
	}
	defer func() {
		c.mu.Lock()
		if c.state.BusyCommand == "daemon-setup" {
			c.state.BusyCommand = ""
		}
		c.mu.Unlock()
	}()
	if repair == nil {
		return errDaemonSetupUnavailable
	}
	setupCtx, cancel := context.WithTimeout(ctx, daemonSetupTimeout)
	defer cancel()
	if err := repair(setupCtx); err != nil {
		return err
	}
	// MSI 安装完成后服务注册到 Windows 服务管理器需要数秒：
	// 轮询等待就绪（总预算受 setupCtx 90s 限制）。
	for {
		if c.daemonHealthy(setupCtx) {
			c.refreshService(setupCtx)
			return nil
		}
		select {
		case <-setupCtx.Done():
			return errDaemonSetupTimeout
		case <-time.After(2 * time.Second):
		}
	}
}

// daemonHealthy 巡检守护进程是否就绪。
func (c *ExpressController) daemonHealthy(ctx context.Context) bool {
	c.mu.Lock()
	inspector := c.service
	c.mu.Unlock()
	if inspector == nil {
		return false
	}
	inspectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	inspection, err := inspector.Inspect(inspectCtx)
	return err == nil && inspection.Health == nbdaemon.ServiceReady
}

// setDaemonError 将就绪失败写入状态（通俗化文案）。
func (c *ExpressController) setDaemonError(err error) {
	c.mu.Lock()
	c.state.Error = daemonPublicError(err)
	c.mu.Unlock()
}

// failDaemonSetup 返回就绪失败的状态快照。
func (c *ExpressController) failDaemonSetup(err error) ExpressState {
	c.setDaemonError(err)
	return c.GetState()
}

// daemonPublicError 把守护进程就绪失败映射为用户可理解的文案。
// 红线：文案不出现"守护进程"字样——对用户而言这是"极速模式"的内部依赖。
func daemonPublicError(err error) *ExpressError {
	switch {
	case errors.Is(err, nbdaemon.ErrElevationCancelled):
		return &ExpressError{Code: expressErrOperationConflict, Message: "准备已取消", Retryable: true, Action: "重试需要管理员授权一次"}
	case errors.Is(err, nbdaemon.ErrArtifactMissing), errors.Is(err, errDaemonSetupUnavailable):
		return &ExpressError{Code: expressErrServiceMissing, Message: "极速模式组件不完整", Retryable: false, Action: "请重新安装 SoGame"}
	case errors.Is(err, nbdaemon.ErrElevationTimedOut), errors.Is(err, errDaemonSetupTimeout):
		return &ExpressError{Code: expressErrInternal, Message: "极速模式准备超时", Retryable: true, Action: "稍后重试"}
	case errors.Is(err, errDaemonSetupGiveUp):
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式准备多次失败", Retryable: true, Action: "请检查安全软件后重试"}
	default:
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式暂时不可用", Retryable: true, Action: "重试"}
	}
}

// ensureDaemonForCommand 房间命令前置保障：守护进程未就绪时先自动补齐
//（透明等待，至多一次 UAC）；失败返回错误状态快照。
// 断开/离开等本地清理命令不经过此函数——它们必须总能执行。
func (c *ExpressController) ensureDaemonForCommand() (ExpressState, bool) {
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.EnsureDaemonReady(ctx); err != nil {
		return c.failDaemonSetup(err), false
	}
	return ExpressState{}, true
}

// runRoomCommand 执行创建/加入房间命令（初始状态为 Enrolling）。
func (c *ExpressController) runRoomCommand(command string, execute func(context.Context) (session.Snapshot, error)) ExpressState {
	return c.runCommandWithInitialState(command, string(session.StateEnrolling), execute)
}

// runCommand 执行房间生命周期命令（连接/断开/离开）。
func (c *ExpressController) runCommand(command string, execute func(context.Context) (session.Snapshot, error)) ExpressState {
	return c.runCommandWithInitialState(command, "", execute)
}

// runCommandWithInitialState 是所有命令的通用执行器，处理并发锁、状态转换和错误映射。
func (c *ExpressController) runCommandWithInitialState(command, initialState string, execute func(context.Context) (session.Snapshot, error)) ExpressState {
	c.mu.Lock()
	if c.state.BusyCommand != "" {
		c.state.Error = &ExpressError{
			Code:      expressErrOperationConflict,
			Message:   "已有房间操作正在进行",
			Retryable: true,
			Action:    "等待当前操作完成",
		}
		state := c.cloneState()
		c.mu.Unlock()
		return state
	}
	c.state.BusyCommand = command
	c.state.Error = nil
	if initialState != "" && c.state.State == string(session.StateNoRoom) {
		c.state.State = initialState
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Unlock()

	snapshot, err := execute(ctx)
	if err == nil {
		c.refreshRoomView(ctx)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.BusyCommand = ""
	if err != nil {
		c.state.State = string(session.StateRecoverableError)
		c.state.ConnectedPath = string(clientnetbird.PathNone)
		c.state.Error = expressPublicError(err)
		logger.Warnf("express: command %q failed: code=%s message=%q cause=%v",
			command, c.state.Error.Code, c.state.Error.Message, err)
		return c.cloneState()
	}
	c.state.State = string(snapshot.State)
	c.state.ConnectedPath = string(snapshot.Path)
	c.state.Error = nil
	if snapshot.State == session.StateNoRoom {
		c.clearActiveRoom()
	}
	return c.cloneState()
}

// failCommand 直接设置错误状态并返回（用于输入校验失败等场景）。
func (c *ExpressController) failCommand(code, message string, retryable bool, action string) ExpressState {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Error = &ExpressError{Code: code, Message: message, Retryable: retryable, Action: action}
	return c.cloneState()
}

// clearActiveRoom 清除与当前房间关联的所有状态字段。
func (c *ExpressController) clearActiveRoom() {
	c.state.RoomID = ""
	c.state.RoomCodeMasked = ""
	c.state.LocalIP = ""
	c.state.Peers = []ExpressPeer{}
	c.state.PeersStale = false
	c.state.HasSavedRoom = false
	c.state.Disconnected = false
	c.state.IsOwner = false
	c.state.RoomCode = ""
	c.state.RelayEnabled = false
	c.state.RelayBlocked = false
}

// refreshRoomView 从 session.Service 拉取最新的房间视图（含对等体列表）。
func (c *ExpressController) refreshRoomView(ctx context.Context) {
	c.mu.Lock()
	rooms := c.rooms
	c.mu.Unlock()
	if rooms == nil {
		return
	}
	view, err := rooms.View(ctx)
	if err != nil {
		if errors.Is(err, session.ErrRoomClosed) {
			// 房间被房主解散(或离线回收):转无房态 + 明确提示;不可重试。
			c.mu.Lock()
			c.state.State = string(session.StateNoRoom)
			c.state.Error = &ExpressError{Code: expressErrRoomClosed, Message: "房主已解散房间", Action: "可创建或加入新房间"}
			c.clearActiveRoom()
			c.mu.Unlock()
			return
		}
		if errors.Is(err, session.ErrStoredStateConflict) || errors.Is(err, session.ErrRoomAlreadySaved) {
			c.mu.Lock()
			c.state.State = string(session.StateRecoverableError)
			c.state.Error = expressPublicError(err)
			c.mu.Unlock()
		}
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.State = string(view.Session.State)
	c.state.ConnectedPath = string(view.Session.Path)
	c.state.RoomID = maskValue(view.Metadata.RoomID)
	c.state.RoomCodeMasked = view.RoomCodeMasked
	c.state.LocalIP = ipHostOnly(view.LocalNetBirdIP)
	c.state.PeersStale = view.PeersStale
	c.state.HasSavedRoom = view.ResumePending
	c.state.Disconnected = view.Disconnected
	c.state.IsOwner = view.IsOwner
	// 安全红线：轮询路径不得把明文房间码塞进状态快照——房间码是入会凭证，
	// 高频快照与事件广播会把它常驻到前端 JS 状态里。完整房间码只经
	// ExpressRevealRoomCode 按需获取（前端复制/展示动作触发），
	// 状态快照中始终为空串。
	c.state.RoomCode = ""
	// 状态机回到非错误状态时同步清除错误横幅,避免出现
	// "已连接"与"错误"同时展示的矛盾界面。
	if view.Session.State != session.StateRecoverableError {
		c.state.Error = nil
	}
	// Build a map from daemon IP → daemon Peer to correctly resolve each room member's connection path.
	daemonByIP := make(map[string]clientnetbird.Peer, len(view.DaemonPeers))
	for _, dp := range view.DaemonPeers {
		if ip := ipHostOnly(dp.NetBirdIP); ip != "" {
			daemonByIP[ip] = dp
		}
	}

	// Relay 允许性由服务器随房间下发；纯 P2P 模式下，底层中继连接被忽略并提示"无法直连"。
	relayEnabled := view.Metadata.RelayEnabled
	c.state.RelayEnabled = relayEnabled
	relayBlocked := false
	c.state.Peers = make([]ExpressPeer, 0, len(view.Peers))
	for _, peer := range view.Peers {
		path := string(clientnetbird.PathNone)
		if dp, ok := daemonByIP[ipHostOnly(peer.NetBirdIP)]; ok {
			path = string(dp.Path)
			if !relayEnabled && dp.Path == clientnetbird.PathRelay {
				path = string(clientnetbird.PathNone)
				relayBlocked = true
			}
		}
		c.state.Peers = append(c.state.Peers, ExpressPeer{
			ID:        peer.ID,
			Name:      peer.Name,
			NetBirdIP: ipHostOnly(peer.NetBirdIP),
			Connected: peer.Connected,
			Path:      path,
		})
	}
	c.state.RelayBlocked = relayBlocked
	// 如果连接状态发生变化，通过 Wails 事件通知前端
	if c.ctx != nil {
		runtime.EventsEmit(c.ctx, "express:state-changed", c.cloneState())
	}
}

// refreshService 检查 NetBird 守护进程的安装与运行状态。
// 健康状态只驱动 daemonNeedsSetup 提示条，不再把"守护进程未安装"等
// 技术文案塞进错误横幅——补齐动作由 EnsureDaemonReady 自动执行。
func (c *ExpressController) refreshService(ctx context.Context) {
	c.mu.Lock()
	inspector := c.service
	c.mu.Unlock()
	if inspector == nil {
		return
	}
	inspection, err := inspector.Inspect(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	needsSetup := inspection.Health != nbdaemon.ServiceReady
	c.state.Service = ExpressService{
		Installed:        inspection.Installed,
		Running:          inspection.Running,
		Version:          inspection.Version,
		ExpectedVersion:  inspection.ExpectedVersion,
		RepairRequired:   needsSetup,
		DaemonNeedsSetup: needsSetup,
	}
	if needsSetup {
		logger.Warnf("express: service health=%s installed=%v running=%v version=%q expected=%q",
			inspection.Health, inspection.Installed, inspection.Running, inspection.Version, inspection.ExpectedVersion)
	}
	// Inspect 调用本身失败（服务查询不可用）仍上浮为错误；健康态不写文案。
	if err != nil && c.state.Error == nil {
		c.state.Error = expressPublicError(err)
	}
}

// cloneState 返回当前状态的深拷贝（避免外部修改影响内部状态）。
func (c *ExpressController) cloneState() ExpressState {
	clone := c.state
	clone.Peers = append([]ExpressPeer(nil), c.state.Peers...)
	clone.DaemonPreparing = c.state.BusyCommand == "daemon-setup"
	return clone
}

// expressPublicError 将内部错误映射为面向用户的 ExpressError。
func expressPublicError(err error) *ExpressError {
	if err == nil {
		return nil
	}
	var httpError *roomapi.HTTPError
	if errors.As(err, &httpError) {
		switch httpError.Code {
		case roomapi.ErrorRoomUnavailable:
			return &ExpressError{Code: expressErrRoomUnavailable, Message: "房间不存在或已不可用", Action: "检查房间码后重试"}
		case roomapi.ErrorRateLimited:
			return &ExpressError{Code: expressErrRoomAPIRateLimited, Message: "请求过于频繁", Retryable: true, Action: "稍后重试"}
		case roomapi.ErrorServiceUnavailable:
			return &ExpressError{Code: expressErrRoomAPIUnavailable, Message: "房间服务暂时不可用", Retryable: true, Action: "稍后重试"}
		}
		return &ExpressError{Code: expressErrInternal, Message: "房间请求未完成", Retryable: httpError.Transient(), Action: "稍后重试"}
	}
	if errors.Is(err, session.ErrRoomClosed) {
		return &ExpressError{Code: expressErrRoomClosed, Message: "房主已解散房间", Action: "可创建或加入新房间"}
	}
	if errors.Is(err, session.ErrRoomAlreadySaved) || errors.Is(err, session.ErrCommandInProgress) {
		return &ExpressError{Code: expressErrOperationConflict, Message: "当前已有一个已保存房间", Action: "先离开当前房间"}
	}
	if errors.Is(err, session.ErrStoredStateConflict) {
		return &ExpressError{Code: expressErrSecureStore, Message: "本地房间数据不完整", Action: "修复或离开当前房间"}
	}
	if errors.Is(err, securestore.ErrNoRoomMetadata) {
		return &ExpressError{Code: expressErrRoomUnavailable, Message: "当前没有已保存的房间", Action: "创建或加入房间"}
	}
	if errors.Is(err, session.ErrSwitchConfirmationRequired) {
		return &ExpressError{Code: expressErrInvalidInput, Message: "切换房间需要确认先离开当前房间", Action: "勾选确认后重试"}
	}
	if errors.Is(err, session.ErrInvalidSwitchMode) {
		return &ExpressError{Code: expressErrInvalidInput, Message: "切换模式无效", Action: "选择创建或加入"}
	}
	var mismatch *clientnetbird.VersionMismatchError
	if errors.As(err, &mismatch) {
		return &ExpressError{Code: expressErrVersionMismatch, Message: "极速模式组件版本不匹配", Action: "将自动补齐"}
	}
	if errors.Is(err, clientnetbird.ErrManagedProfileConflict) || errors.Is(err, clientnetbird.ErrManagedProfileInconsistent) {
		return &ExpressError{Code: expressErrProfileConflict, Message: "极速模式组件配置不一致", Action: "重试"}
	}
	if errors.Is(err, nbdaemon.ErrServiceMissing) {
		return &ExpressError{Code: expressErrServiceMissing, Message: "极速模式组件未安装", Retryable: true, Action: "将自动补齐"}
	}
	if errors.Is(err, nbdaemon.ErrServiceUnavailable) {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式组件暂时不可用", Retryable: true, Action: "重试"}
	}
	if errors.Is(err, nbdaemon.ErrServiceAccess) {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "无法读取极速模式组件状态", Retryable: true, Action: "重试"}
	}
	if errors.Is(err, nbdaemon.ErrElevationCancelled) {
		return &ExpressError{Code: expressErrOperationConflict, Message: "准备已取消", Retryable: true, Action: "重试需要管理员授权一次"}
	}
	if errors.Is(err, nbdaemon.ErrElevationTimedOut) {
		return &ExpressError{Code: expressErrInternal, Message: "极速模式准备超时", Retryable: true, Action: "稍后重试"}
	}
	if strings.Contains(strings.ToLower(err.Error()), "room session is unavailable") {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式尚未就绪", Retryable: true, Action: "稍后重试"}
	}
	// 兜底文案不得写死"加入房间失败"：create/disconnect/leave/repair 等
	// 命令的未识别错误都会走到这里。
	return &ExpressError{Code: expressErrEnrollmentFailed, Message: "操作未完成", Retryable: true, Action: "稍后重试"}
}

// maskValue 对短字符串进行脱敏（仅保留前 4 位 + ****）。
func maskValue(value string) string {
	if len(value) <= 4 {
		return value
	}
	return value[:4] + "****"
}

// ipHostOnly 去掉 IP 的 CIDR 前缀（daemon 报告的本机 IP 形如 100.66.172.6/16）。
func ipHostOnly(value string) string {
	if index := strings.IndexByte(value, '/'); index >= 0 {
		return value[:index]
	}
	return value
}

// StartExpressStateRefresh 启动定时刷新协程，定期更新房间视图和服务状态。
// 当极速模式处于前台时，每 5 秒刷新一次对等体列表。
func (c *ExpressController) StartExpressStateRefresh(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refreshRoomView(ctx)
			}
		}
	}()
}
