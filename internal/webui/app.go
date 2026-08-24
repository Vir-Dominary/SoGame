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
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"sogame/internal/config"
	"sogame/internal/logger"
	"sogame/internal/n2n"
	"sogame/internal/platform"
)

type AppState string

const (
	StateDisconnected AppState = "disconnected"
	StateConnecting   AppState = "connecting"
	StateConnected    AppState = "connected"
	StateFailed       AppState = "failed"
)

// 联机模式
type AppMode string

const (
	ModeClassic AppMode = "classic" // 经典模式：n2n + tap 网卡
	ModeExpress AppMode = "express" // 极速模式：netbird + wireguard
)

type App struct {
	mu      sync.Mutex
	ctx     context.Context
	edge    *n2n.Edge
	cfg     *config.Config
	state   AppState
	errMsg  string
	express *ExpressController
}

func NewApp() *App {
	cfg, err := config.LoadOrCreate()
	if err != nil {
		logger.Errorf("failed to load config: %v", err)
		cfg = config.DefaultConfig()
	}

	// 构造极速模式控制器。NetBird 守护进程不可用时控制器会携带错误状态，
	// 前端通过 ExpressGetState() 读取错误并引导用户修复。
	roomAPIURL := cfg.RoomAPIURL
	if roomAPIURL == "" {
		roomAPIURL = config.DefaultRoomAPIURL
	}
	express := NewWindowsExpressController(roomAPIURL)

	return &App{
		edge:    &n2n.Edge{},
		cfg:     cfg,
		state:   StateDisconnected,
		express: express,
	}
}

func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	// 启动极速模式控制器（后台刷新房间视图与守护进程状态）
	if a.express != nil {
		a.express.Startup(ctx)
		a.express.StartExpressStateRefresh(ctx)
	}
}

func (a *App) Shutdown(ctx context.Context) {
	a.mu.Lock()
	edge := a.edge
	express := a.express
	a.mu.Unlock()

	if edge != nil {
		if err := edge.Stop(); err != nil {
			logger.Warnf("shutdown: failed to stop edge: %v", err)
		} else {
			logger.Infof("shutdown: edge process stopped")
		}
	}
	if err := n2n.KillOrphanEdgeProcess(); err != nil {
		logger.Warnf("shutdown: failed to kill orphan edge process: %v", err)
	}

	if express != nil {
		express.Shutdown(ctx)
	}
}

func (a *App) GetState() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.state)
}

func (a *App) GetErrorMessage() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.errMsg
}

type ConfigInfo struct {
	Community string `json:"community"`
	IP        string `json:"ip"`
	KeyMasked string `json:"key_masked"`
	KeySet    bool   `json:"key_set"`
	Supernode string `json:"supernode"`
}

func maskKey(key string) string {
	if key == "" {
		return "(none)"
	}
	if len(key) <= 4 {
		return "****"
	}
	return key[:2] + "****" + key[len(key)-2:]
}

func (a *App) GetConfig() ConfigInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return ConfigInfo{
		Community: a.cfg.Community,
		IP:        a.cfg.IP,
		KeyMasked: maskKey(a.cfg.Key),
		KeySet:    a.cfg.Key != "",
		Supernode: a.cfg.Supernode,
	}
}

type NodeInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type NodeLatencyInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Latency int    `json:"latency"`
}

func (a *App) GetNodes() []NodeInfo {
	nodes := n2n.GetKnownNodes()
	result := make([]NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, NodeInfo{Name: node.Name, Address: node.Address})
	}
	return result
}

func (a *App) GetNodesWithLatency() []NodeLatencyInfo {
	nodes := n2n.GetKnownNodes()
	out := make([]NodeLatencyInfo, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, NodeLatencyInfo{
			Name:    node.Name,
			Address: node.Address,
			Latency: -2,
		})
	}

	go func() {
		results := n2n.MeasureAllNodesLatency()
		updated := make([]NodeLatencyInfo, 0, len(results))
		for _, r := range results {
			updated = append(updated, NodeLatencyInfo{
				Name:    r.Name,
				Address: r.Address,
				Latency: r.Latency,
			})
		}
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, "nodeLatencyUpdated", updated)
		}
	}()

	return out
}

type inviteData struct {
	Community string `json:"c"`
	Key       string `json:"k"`
	Supernode string `json:"s"`
}

const communityPrefix = "community-"

// encodeInvite 将邀请数据编码为邀请码。
// 优先使用 v2 紧凑格式（base64url + "|" 分隔），不满足条件时回退到 v1 JSON 格式。
func encodeInvite(data inviteData) (string, error) {
	// 尝试 v2 格式：仅适用于标准 "community-XXX" 社区名 + hex 密钥
	if strings.HasPrefix(data.Community, communityPrefix) {
		keyBytes, err := hex.DecodeString(data.Key)
		if err == nil {
			communityShort := data.Community[len(communityPrefix):]
			keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)
			inner := communityShort + "|" + keyB64 + "|" + data.Supernode
			return base64.RawURLEncoding.EncodeToString([]byte(inner)), nil
		}
	}

	// 回退到 v1 JSON 格式
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("生成邀请码失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(jsonBytes), nil
}

// decodeInvite 解码邀请码，同时支持 v1（JSON + standard base64）和 v2（紧凑格式）。
func decodeInvite(code string) (*inviteData, error) {
	// 尝试 v1：standard base64 + JSON
	if decoded, err := base64.StdEncoding.DecodeString(code); err == nil {
		s := string(decoded)
		if strings.HasPrefix(s, "{") {
			var data inviteData
			if err := json.Unmarshal(decoded, &data); err == nil {
				return &data, nil
			}
		}
		// 也可能是用 standard base64 编码的 v2 格式
		if data, err := parseInviteV2(s); err == nil {
			return data, nil
		}
	}

	// 尝试 v2：base64url（无填充）
	if decoded, err := base64.RawURLEncoding.DecodeString(code); err == nil {
		if data, err := parseInviteV2(string(decoded)); err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("无效的邀请码格式")
}

// parseInviteV2 解析 v2 格式的邀请码内容（已 base64 解码后的字符串）。
// 格式：<community_short>|<key_base64url>|<supernode>
func parseInviteV2(s string) (*inviteData, error) {
	parts := strings.SplitN(s, "|", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("v2 格式字段数不足")
	}

	keyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("v2 密钥解码失败: %w", err)
	}

	return &inviteData{
		Community: communityPrefix + parts[0],
		Key:       hex.EncodeToString(keyBytes),
		Supernode: parts[2],
	}, nil
}

func getStableDeviceID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	exePath, err := os.Executable()
	if err != nil {
		exePath = "default"
	}
	h := sha256.New()
	h.Write([]byte(hostname + exePath))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func generateStableIP(deviceID, community string) string {
	h := sha256.New()
	h.Write([]byte(deviceID + community))
	hash := hex.EncodeToString(h.Sum(nil))
	b, _ := hex.DecodeString(hash[:4])
	host := int(b[0])%254 + 1
	return fmt.Sprintf("10.10.10.%d", host)
}

func (a *App) GenerateInvite(supernode string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Community == "" {
		return "", fmt.Errorf("社区名为空")
	}

	if a.cfg.Key == "" {
		keyBytes := make([]byte, 16)
		if _, err := rand.Read(keyBytes); err != nil {
			return "", fmt.Errorf("生成密钥失败: %w", err)
		}
		a.cfg.Key = hex.EncodeToString(keyBytes)
		if err := config.SaveCached(a.cfg); err != nil {
			return "", fmt.Errorf("保存密钥失败: %w", err)
		}
		logger.Infof("自动生成房间密钥: %s", maskKey(a.cfg.Key))
	}

	data := inviteData{
		Community: a.cfg.Community,
		Key:       a.cfg.Key,
		Supernode: supernode,
	}

	return encodeInvite(data)
}

func (a *App) ConnectWithInvite(code string) error {
	data, err := decodeInvite(code)
	if err != nil {
		return fmt.Errorf("邀请码解析失败: %w", err)
	}

	if data.Community == "" {
		return fmt.Errorf("邀请码中缺少群名")
	}
	if data.Supernode == "" {
		return fmt.Errorf("邀请码中缺少中心节点")
	}

	deviceID := getStableDeviceID()
	ip := generateStableIP(deviceID, data.Community)

	logger.Infof("邀请码解析成功:")
	logger.Infof("  群名: %s", data.Community)
	logger.Infof("  中心节点: %s", n2n.MaskSupernode(data.Supernode))
	if data.Key != "" {
		logger.Infof("  密钥: %s", maskKey(data.Key))
	}
	logger.Infof("  分配IP: %s", ip)

	return a.Connect(data.Community, ip, data.Key, data.Supernode)
}

func (a *App) Connect(community, ip, key, supernode string) error {
	a.mu.Lock()
	a.state = StateConnecting
	a.errMsg = ""
	a.cfg.Community = community
	a.cfg.IP = ip
	if key != "" {
		a.cfg.Key = key
	}
	a.cfg.Supernode = supernode
	a.mu.Unlock()

	if err := config.SaveCached(a.cfg); err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("保存配置失败: %v", err)
		a.mu.Unlock()
		return fmt.Errorf("保存配置失败: %w", err)
	}

	if a.cfg.Key == "" {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = "请先设置密码"
		a.mu.Unlock()
		return fmt.Errorf("请先设置密码")
	}

	status, err := platform.EnsureSoGameAdapter()
	if err != nil || (status != platform.TapInstallSuccess && status != platform.TapAlreadyInstalled) {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("网络适配器安装失败: %v", err)
		a.mu.Unlock()
		return fmt.Errorf("网络适配器安装失败: %w", err)
	}

	a.edge.SetConnectionStateCallback(func(state n2n.ConnectionState) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch state {
		case n2n.StateConnecting:
			a.state = StateConnecting
			a.errMsg = ""
		case n2n.StateConnected:
			a.state = StateConnecting
			a.errMsg = ""
		case n2n.StateRegistered:
			a.state = StateConnected
			a.errMsg = ""
		case n2n.StateError:
			a.state = StateFailed
			a.errMsg = "连接过程中发生错误"
		case n2n.StateDisconnected:
			a.state = StateDisconnected
			a.errMsg = ""
		}
	})

	a.edge.SetStatusCallback(func(isRunning bool, message string) {
		if !isRunning {
			a.mu.Lock()
			a.state = StateDisconnected
			a.errMsg = message
			a.mu.Unlock()
		}
	})

	a.mu.Lock()
	a.state = StateConnecting
	a.mu.Unlock()

	err = a.edge.Start(a.cfg)
	if err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("连接失败: %v", err)
		a.mu.Unlock()
		return fmt.Errorf("连接失败: %w", err)
	}

	return nil
}

func (a *App) Disconnect() error {
	err := a.edge.Stop()
	if err != nil {
		a.mu.Lock()
		a.errMsg = fmt.Sprintf("断开失败: %v", err)
		a.mu.Unlock()
		return err
	}

	if fwErr := platform.RemoveFirewallRule(); fwErr != nil {
		logger.Warnf("断开连接时移除防火墙规则失败: %v", fwErr)
	}

	a.mu.Lock()
	a.state = StateDisconnected
	a.errMsg = ""
	a.mu.Unlock()
	return nil
}

func (a *App) IsNetworkAdapterReady() bool {
	return platform.IsNetworkAdapterReady()
}

func (a *App) OpenLogs() error {
	logFile := logger.GetLogFile()
	if _, err := os.Stat(logFile); err != nil {
		return fmt.Errorf("日志文件不存在: %s", logFile)
	}
	cmd := exec.Command("notepad.exe", logFile)
	return cmd.Start()
}

type AboutInfo struct {
	AppName       string `json:"appName"`
	AppVersion    string `json:"appVersion"`
	AppAuthor     string `json:"appAuthor"`
	AppURL        string `json:"appURL"`
	AppBilibili   string `json:"bilibiliURL"`
	AppDesc       string `json:"appDesc"`
	AppSponsorURL string `json:"sponsorURL"`
}

type ConnectionDetails struct {
	Connected  bool   `json:"connected"`
	VirtualIP  string `json:"virtualIP"`
	NodeName   string `json:"nodeName"`
	Status     string `json:"status"`
	SponsorURL string `json:"sponsorURL"`
}

func (a *App) GetConnectionDetails() ConnectionDetails {
	a.mu.Lock()
	mode := AppMode(a.cfg.Mode)
	if mode == "" {
		mode = ModeClassic
	}
	a.mu.Unlock()

	// 极速模式：从 express 控制器获取真实状态
	if mode == ModeExpress && a.express != nil {
		expressState := a.express.GetState()
		connected := expressState.State == "ConnectedP2P" || expressState.State == "ConnectedRelay"
		details := ConnectionDetails{
			Connected:  connected,
			VirtualIP:  expressState.LocalIP,
			NodeName:   "NetBird",
			SponsorURL: config.AppSponsorURL,
		}
		switch {
		case connected:
			details.Status = "正常"
		case expressState.State == "Enrolling" || expressState.State == "ConnectingPeer" || expressState.State == "Reconnecting":
			details.Status = "连接中"
		case expressState.State == "RecoverableError":
			details.Status = "异常"
		default:
			details.Status = "未连接"
		}
		return details
	}

	// 经典模式
	a.mu.Lock()
	defer a.mu.Unlock()

	details := ConnectionDetails{
		Connected:  a.state == StateConnected,
		VirtualIP:  a.cfg.IP,
		NodeName:   n2n.LookupNodeName(a.cfg.Supernode),
		SponsorURL: config.AppSponsorURL,
	}

	if details.NodeName == "" {
		details.NodeName = n2n.MaskSupernode(a.cfg.Supernode)
	}

	switch a.state {
	case StateConnected:
		details.Status = "正常"
	case StateConnecting:
		details.Status = "连接中"
	case StateFailed:
		details.Status = "异常"
	default:
		details.Status = "未连接"
	}

	return details
}

func (a *App) GetAboutInfo() AboutInfo {
	return AboutInfo{
		AppName:       config.AppName,
		AppVersion:    config.AppVersion,
		AppAuthor:     config.AppAuthor,
		AppURL:        config.AppURL,
		AppBilibili:   config.AppBilibili,
		AppDesc:       config.AppDesc,
		AppSponsorURL: config.AppSponsorURL,
	}
}

func (a *App) GetLogContent() string {
	if err := logger.Init(); err != nil {
		return fmt.Sprintf("初始化日志失败: %v", err)
	}
	content, err := logger.GetLogContent(200)
	if err != nil {
		return fmt.Sprintf("读取日志失败: %v", err)
	}
	return content
}

// ============================================================================
// 联机模式 API
// ============================================================================

// ModeInfo 返回当前模式与极速模式配置
type ModeInfo struct {
	Current   string `json:"current"`   // classic / express
	Nickname  string `json:"nickname"`  // 极速模式昵称
	RoomAPIURL string `json:"roomApiUrl"` // Room API 服务地址
}

// GetMode 返回当前联机模式
func (a *App) GetMode() ModeInfo {
	a.mu.Lock()
	mode := a.cfg.Mode
	if mode == "" {
		mode = string(ModeClassic)
	}
	nickname := a.cfg.ExpressNickname
	roomAPIURL := a.cfg.RoomAPIURL
	a.mu.Unlock()

	if roomAPIURL == "" {
		roomAPIURL = config.DefaultRoomAPIURL
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}

	return ModeInfo{
		Current:    mode,
		Nickname:   nickname,
		RoomAPIURL: roomAPIURL,
	}
}

// SetMode 切换联机模式。如果当前已连接，将先断开。
func (a *App) SetMode(mode string) error {
	if mode != string(ModeClassic) && mode != string(ModeExpress) {
		return fmt.Errorf("无效的模式: %s（仅支持 classic 或 express）", mode)
	}

	a.mu.Lock()
	connected := a.state == StateConnected || a.state == StateConnecting
	oldMode := AppMode(a.cfg.Mode)
	if oldMode == "" {
		oldMode = ModeClassic
	}
	a.mu.Unlock()

	// 模式切换且当前已连接，先断开
	if connected && oldMode != AppMode(mode) {
		if oldMode == ModeClassic {
			_ = a.Disconnect()
		} else if a.express != nil {
			_ = a.express.Disconnect()
		}
	}

	a.mu.Lock()
	a.cfg.Mode = mode
	a.mu.Unlock()

	if err := config.SaveCached(a.cfg); err != nil {
		return fmt.Errorf("保存模式失败: %w", err)
	}
	logger.Infof("mode switched to: %s", mode)
	return nil
}

// SaveExpressSettings 保存极速模式的配置（Room API 地址、昵称）
func (a *App) SaveExpressSettings(roomAPIURL, nickname string) error {
	if nickname == "" {
		return fmt.Errorf("昵称不能为空")
	}
	if roomAPIURL == "" {
		roomAPIURL = config.DefaultRoomAPIURL
	}

	a.mu.Lock()
	a.cfg.RoomAPIURL = roomAPIURL
	a.cfg.ExpressNickname = nickname
	a.mu.Unlock()

	return config.SaveCached(a.cfg)
}

// ============================================================================
// 极速模式 API（封装对 ExpressController 的调用，暴露给 Wails 前端）
// ============================================================================

// ExpressGetState 返回极速模式的当前状态快照
func (a *App) ExpressGetState() ExpressState {
	if a.express == nil {
		return ExpressState{
			State: "RecoverableError",
			Error: &ExpressError{
				Code:    expressErrServiceUnavailable,
				Message: "极速模式不可用",
				Action:  "请检查守护进程",
			},
		}
	}
	return a.express.GetState()
}

// ExpressCreateRoom 创建房间（极速模式 - 创建者）
func (a *App) ExpressCreateRoom(nickname string) ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	if strings.TrimSpace(nickname) == "" {
		nickname = a.cfg.ExpressNickname
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}
	// 持久化昵称
	a.mu.Lock()
	a.cfg.ExpressNickname = nickname
	a.mu.Unlock()
	_ = config.SaveCached(a.cfg)
	return a.express.CreateRoom(nickname)
}

// ExpressJoinRoom 加入房间（极速模式 - 加入者）
func (a *App) ExpressJoinRoom(roomCode, nickname string) ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	if strings.TrimSpace(nickname) == "" {
		nickname = a.cfg.ExpressNickname
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}
	a.mu.Lock()
	a.cfg.ExpressNickname = nickname
	a.mu.Unlock()
	_ = config.SaveCached(a.cfg)
	return a.express.JoinRoom(roomCode, nickname)
}

// ExpressDisconnect 断开当前房间连接（保留房间记录）
func (a *App) ExpressDisconnect() ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	return a.express.Disconnect()
}

// ExpressReconnect 重新连接到已保存的房间
func (a *App) ExpressReconnect() ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	return a.express.Reconnect()
}

// ExpressLeaveRoom 离开房间，清除本地房间数据
func (a *App) ExpressLeaveRoom() ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	return a.express.LeaveRoom()
}

// ExpressRevealRoomCode 返回完整的房间码（供用户分享）
func (a *App) ExpressRevealRoomCode() (string, *ExpressError) {
	if a.express == nil {
		return "", &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}
	}
	return a.express.RevealRoomCode()
}

// ExpressRepairService 修复/重新安装 NetBird 守护进程
func (a *App) ExpressRepairService() ExpressState {
	if a.express == nil {
		return ExpressState{State: "RecoverableError", Error: &ExpressError{Code: expressErrServiceUnavailable, Message: "极速模式不可用"}}
	}
	return a.express.RepairService()
}
