package app

import (
	"context"
	"errors"
	"fmt"
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
	Disconnected   bool           `json:"disconnected"`
	RoomCode       string         `json:"roomCode"`   // 用户已主动断开（可重新连接）
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
}

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

// hasError 返回控制器是否处于错误状态。
func (c *ExpressController) hasError() bool {
	return c.state.Error != nil
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

// Shutdown 在 Wails 关闭时调用，释放 RPC 连接。
func (c *ExpressController) Shutdown(_ context.Context) {
	c.mu.Lock()
	closeFn := c.close
	c.mu.Unlock()
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

// RepairService 修复/重新安装 NetBird 守护进程。
func (c *ExpressController) RepairService() ExpressState {
	c.mu.Lock()
	repair := c.repair
	ctx := c.ctx
	c.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if repair == nil {
		c.mu.Lock()
		c.state.Error = &ExpressError{Code: expressErrServiceMissing, Message: "守护进程未安装", Retryable: true, Action: "重新安装守护进程"}
		c.state.Service.RepairRequired = true
		state := c.cloneState()
		c.mu.Unlock()
		return state
	}
	c.mu.Lock()
	c.state.BusyCommand = "repair"
	c.state.Error = nil
	c.mu.Unlock()
	err := repair(ctx)
	c.mu.Lock()
	c.state.BusyCommand = ""
	if err != nil {
		c.state.Error = expressPublicError(err)
		c.state.Service.RepairRequired = true
		state := c.cloneState()
		c.mu.Unlock()
		return state
	}
	c.state.Error = nil
	c.mu.Unlock()
	// 安装/修复完成：同步复查守护进程状态，让返回的状态快照直接反映
	// 安装结果（前端据此提示"守护进程安装完毕"或安装失败）。
	c.refreshService(ctx)
	c.mu.Lock()
	if !c.state.Service.Installed {
		// msiexec 成功但服务仍不存在（安装被回滚/服务未创建）：
		// 给出明确的失败提示，而不是让"守护进程未安装"报错反复出现。
		c.state.Error = &ExpressError{
			Code:      expressErrServiceMissing,
			Message:   "守护进程安装未完成，请查看运行日志",
			Retryable: true,
			Action:    "重新安装",
		}
		c.state.Service.RepairRequired = true
	} else if c.state.Error != nil {
		// 已安装但守护进程尚未就绪（启动需要数秒）：先不报错，
		// 由后台 waitForDaemonAndReassemble 在就绪后刷新状态，
		// 避免"安装完毕"与"未运行"同时展示的矛盾界面。
		c.state.Error = nil
	}
	state := c.cloneState()
	c.mu.Unlock()
	return state
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
	c.state.RoomCode = ""
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
	c.state.RoomCode = ""
	if view.Session.State != session.StateNoRoom && view.Session.State != session.StateEnrolling {
		if code, err := rooms.RevealRoomCode(ctx); err == nil {
			c.state.RoomCode = code
		}
	}
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

	c.state.Peers = make([]ExpressPeer, 0, len(view.Peers))
	for _, peer := range view.Peers {
		path := string(clientnetbird.PathNone)
		if dp, ok := daemonByIP[ipHostOnly(peer.NetBirdIP)]; ok {
			path = string(dp.Path)
		}
		c.state.Peers = append(c.state.Peers, ExpressPeer{
			ID:        peer.ID,
			Name:      peer.Name,
			NetBirdIP: ipHostOnly(peer.NetBirdIP),
			Connected: peer.Connected,
			Path:      path,
		})
	}
	// 如果连接状态发生变化，通过 Wails 事件通知前端
	if c.ctx != nil {
		runtime.EventsEmit(c.ctx, "express:state-changed", c.cloneState())
	}
}

// refreshService 检查 NetBird 守护进程的安装与运行状态。
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
	c.state.Service = ExpressService{
		Installed:       inspection.Installed,
		Running:         inspection.Running,
		Version:         inspection.Version,
		ExpectedVersion: inspection.ExpectedVersion,
		RepairRequired:  inspection.Health != nbdaemon.ServiceReady,
	}
	if inspection.Health != nbdaemon.ServiceReady {
		logger.Warnf("express: service health=%s installed=%v running=%v version=%q expected=%q",
			inspection.Health, inspection.Installed, inspection.Running, inspection.Version, inspection.ExpectedVersion)
	}
	if err != nil && c.state.Error == nil {
		c.state.Error = expressPublicError(err)
	}
	if c.state.Error == nil {
		switch inspection.Health {
		case nbdaemon.ServiceMissing:
			c.state.Error = &ExpressError{Code: expressErrServiceMissing, Message: "守护进程未安装", Retryable: true, Action: "重新安装守护进程"}
		case nbdaemon.ServiceVersionMismatch:
			c.state.Error = &ExpressError{Code: expressErrVersionMismatch, Message: "守护进程版本不匹配", Action: "重新安装守护进程"}
		case nbdaemon.ServiceStopped, nbdaemon.ServiceUnhealthy:
			c.state.Error = &ExpressError{Code: expressErrServiceUnavailable, Message: "守护进程未运行", Retryable: true, Action: "修复或启动守护进程"}
		}
	}
}

// cloneState 返回当前状态的深拷贝（避免外部修改影响内部状态）。
func (c *ExpressController) cloneState() ExpressState {
	clone := c.state
	clone.Peers = append([]ExpressPeer(nil), c.state.Peers...)
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
		return &ExpressError{Code: expressErrVersionMismatch, Message: fmt.Sprintf("守护进程版本不匹配，需要 v%s", mismatch.Expected), Action: "重新安装守护进程"}
	}
	if errors.Is(err, clientnetbird.ErrManagedProfileConflict) || errors.Is(err, clientnetbird.ErrManagedProfileInconsistent) {
		return &ExpressError{Code: expressErrProfileConflict, Message: "守护进程配置与本地房间不一致", Action: "重新安装守护进程"}
	}
	if errors.Is(err, nbdaemon.ErrServiceMissing) {
		return &ExpressError{Code: expressErrServiceMissing, Message: "守护进程未安装", Retryable: true, Action: "重新安装守护进程"}
	}
	if errors.Is(err, nbdaemon.ErrServiceUnavailable) {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "守护进程暂时不可用", Retryable: true, Action: "检查守护进程或重试"}
	}
	if errors.Is(err, nbdaemon.ErrServiceAccess) {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "无法读取守护进程状态", Retryable: true, Action: "重试或重新安装"}
	}
	if errors.Is(err, nbdaemon.ErrElevationCancelled) {
		return &ExpressError{Code: expressErrOperationConflict, Message: "已取消安装", Action: "可稍后重新点击安装"}
	}
	if errors.Is(err, nbdaemon.ErrElevationTimedOut) {
		return &ExpressError{Code: expressErrInternal, Message: "安装未在规定时间内完成", Retryable: true, Action: "稍后重试"}
	}
	if strings.Contains(strings.ToLower(err.Error()), "room session is unavailable") {
		return &ExpressError{Code: expressErrServiceUnavailable, Message: "守护进程不可用", Retryable: true, Action: "检查后重试"}
	}
	return &ExpressError{Code: expressErrEnrollmentFailed, Message: "加入房间失败", Retryable: true, Action: "稍后重试"}
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
