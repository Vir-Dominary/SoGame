package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	ModeExpress AppMode = "express" // 极速模式：wireguard + wintun
)

// agentListenAddr 是 sogame-agent HTTP 监听地址
const agentListenAddr = "127.0.0.1:7890"
const agentBaseURL = "http://" + agentListenAddr

// DefaultWGServerURL 是 WireGuard 控制服务器的默认地址（官方服务器公网地址）。
const DefaultWGServerURL = "http://123.56.254.224"

// normalizeWGServerURL 确保服务器地址包含 http:// 或 https:// 协议前缀。
// 用户可能输入 "127.0.0.1:8080" 这样的裸地址，缺少协议前缀会导致 Go net/http
// 将其当作相对路径解析，触发 "first path segment in URL cannot contain colon" 错误。
func normalizeWGServerURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return url
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	return "http://" + url
}

// isAgentRunning 检测 Agent 是否在监听（含外部独立启动的 Agent）。
// 通过 HTTP 探测 /api/agent/status，比仅检查 agentCmd 更可靠：
// 用户可能通过 start-wg-test.ps1 提权启动 Agent，此时 agentCmd 为 nil
// 但 Agent 实际已运行。
func isAgentRunning() bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(agentBaseURL + "/api/agent/status")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type App struct {
	mu       sync.Mutex
	ctx      context.Context
	edge     *n2n.Edge
	cfg      *config.Config
	state    AppState
	errMsg   string
	agentCmd *exec.Cmd
}

func NewApp() *App {
	cfg, err := config.LoadOrCreate()
	if err != nil {
		logger.Errorf("failed to load config: %v", err)
		cfg = config.DefaultConfig()
	}

	return &App{
		edge:  &n2n.Edge{},
		cfg:   cfg,
		state: StateDisconnected,
	}
}

func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	// 启动 WireGuard Agent 子进程（极速模式使用，经典模式下保持空闲）
	go a.startAgent()
}

func (a *App) Shutdown(ctx context.Context) {
	a.mu.Lock()
	edge := a.edge
	a.mu.Unlock()

	if edge != nil {
		if err := edge.Stop(); err != nil {
			logger.Warnf("shutdown: failed to stop edge: %v", err)
		} else {
			logger.Infof("shutdown: edge process stopped")
		}
	}
	// 清理可能遗留的孤儿进程（仅清理本应用启动的）
	if err := n2n.KillOrphanEdgeProcess(); err != nil {
		logger.Warnf("shutdown: failed to kill orphan edge process: %v", err)
	}

	// 停止 WireGuard Agent 子进程
	a.stopAgent()
}

// resolveAgentBinDir 解析 sogame-agent.exe 和 wireguard.exe/wg.exe 所在目录
// 搜索顺序（binDir 同时是 wireguard.exe/wg.exe 所在目录）：
//  1. {exe_dir}/bin/sogame-agent.exe        —— 安装后（iss 打包到 {app}\bin\）
//  2. {exe_dir}/sogame-agent.exe            —— 开发模式（手动拷贝到 build\bin\）
//  3. 向上查找 wireguard/agent/sogame-agent.exe —— 开发模式回退（wails dev/build）
func resolveAgentBinDir() (binDir, agentExePath string) {
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		// 1. 安装后：{app}\SoGame.exe，agent 在 {app}\bin\sogame-agent.exe
		binDirCandidate := filepath.Join(exeDir, "bin")
		agentCandidate := filepath.Join(binDirCandidate, "sogame-agent.exe")
		if _, err := os.Stat(agentCandidate); err == nil {
			return binDirCandidate, agentCandidate
		}
		// 2. 开发模式：exe 同目录
		agentCandidate = filepath.Join(exeDir, "sogame-agent.exe")
		if _, err := os.Stat(agentCandidate); err == nil {
			return exeDir, agentCandidate
		}
		// 3. 开发模式回退：向上查找 wireguard/agent/sogame-agent.exe
		// 适用于 wails dev/build 时 SoGame.exe 在 build\bin\，wireguard 在项目根目录
		d := exeDir
		for i := 0; i < 6; i++ {
			candidate := filepath.Join(d, "wireguard", "agent", "sogame-agent.exe")
			if _, err := os.Stat(candidate); err == nil {
				// binDir 指向包含 wireguard.exe/wg.exe 的目录
				return filepath.Join(d, "wireguard"), candidate
			}
			parent := filepath.Dir(d)
			if parent == d {
				break // 到达根目录
			}
			d = parent
		}
	}
	return "", ""
}

// startAgent 启动 sogame-agent 子进程
// Agent 运行 HTTP 服务（127.0.0.1:7890），由 Wails 主程序通过 HTTP 调用。
// 若检测到已有 Agent 在监听（如用户通过 start-wg-test.ps1 独立提权启动），
// 则跳过启动，直接复用外部 Agent。
func (a *App) startAgent() {
	// 检测是否已有外部 Agent 在运行（端口 7890 已被占用）
	if isAgentRunning() {
		logger.Infof("agent: external agent already running on %s, skip launching", agentListenAddr)
		return
	}

	binDir, agentExePath := resolveAgentBinDir()
	if agentExePath == "" {
		logger.Warnf("agent: sogame-agent.exe not found, express mode unavailable")
		return
	}

	cmd := exec.Command(agentExePath)
	cmd.Env = append(os.Environ(),
		"SOGAME_BIN_DIR="+binDir,
		"SOGAME_AGENT_LISTEN="+agentListenAddr,
	)
	// 隐藏子进程控制台窗口，让用户无感知
	hideConsoleProcess(cmd)
	// Agent 自带 logger 写入独立日志文件，丢弃 stdout/stderr 避免 GUI 程序句柄无效
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		logger.Errorf("failed to start agent: %v", err)
		return
	}

	a.mu.Lock()
	a.agentCmd = cmd
	a.mu.Unlock()
	logger.Infof("agent started, pid=%d, binDir=%s", cmd.Process.Pid, binDir)

	// 等待子进程退出（正常情况下会一直阻塞到 Shutdown）
	err := cmd.Wait()
	if err != nil {
		logger.Warnf("agent exited: %v", err)
	}

	a.mu.Lock()
	a.agentCmd = nil
	a.mu.Unlock()
}

// stopAgent 停止 Agent 子进程：先 HTTP 通知断开 WireGuard，再 Kill 进程
// Windows 上 SIGTERM 信号不可靠，因此先调用 /disconnect API 触发 Agent 的 cleanup()
// 清理 WireGuard 接口，然后直接 Kill 终止进程
func (a *App) stopAgent() {
	a.mu.Lock()
	cmd := a.agentCmd
	a.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	// 1. 通过 HTTP 通知 Agent 断开 WireGuard 连接（清理接口）
	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Post(agentBaseURL+"/api/agent/disconnect", "application/json", bytes.NewReader([]byte("{}"))); err == nil {
		_ = resp.Body.Close()
		logger.Infof("agent: disconnect requested")
	}

	// 2. 等待 1 秒让 Agent 完成 cleanup
	time.Sleep(1 * time.Second)

	// 3. 强制终止子进程
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	logger.Infof("agent stopped")
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
	// 先获取节点列表（不测量延迟），确保 UI 能立即显示
	nodes := n2n.GetKnownNodes()
	out := make([]NodeLatencyInfo, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, NodeLatencyInfo{
			Name:    node.Name,
			Address: node.Address,
			Latency: -2, // -2 表示尚未测量
		})
	}

	// 异步测量延迟，通过事件通知前端更新
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
		// 通过 Wails 事件系统通知前端延迟数据已更新
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

// communityPrefix 是自动生成的社区名前缀。
// v2 邀请码格式会省略此前缀以缩短长度，解码时再补回。
const communityPrefix = "community-"

// encodeInvite 将邀请数据编码为邀请码。
// 优先使用 v2 紧凑格式（base64url + "|" 分隔），不满足条件时回退到 v1 JSON 格式。
// v2 格式比 v1 短约 40%，通过省略 "community-" 前缀和用 base64url 编码密钥实现。
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

// generateStableIP 基于设备 ID 和社区名生成稳定的虚拟 IP。
// 仅随机化最后 8 位（10.10.10.X），配合 /24 子网掩码，避免 Windows
// 将子网掩码强制改为 255.255.255.0 导致不同网段用户无法通信的问题。
func generateStableIP(deviceID, community string) string {
	h := sha256.New()
	h.Write([]byte(deviceID + community))
	hash := hex.EncodeToString(h.Sum(nil))
	b, _ := hex.DecodeString(hash[:4])
	host := int(b[0])%254 + 1 // 1-254
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

	// 在启动 edge 之前设置回调，因为 edge 可能在 Start() 返回前就输出注册成功
	a.edge.SetConnectionStateCallback(func(state n2n.ConnectionState) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch state {
		case n2n.StateConnecting:
			a.state = StateConnecting
			a.errMsg = ""
		case n2n.StateConnected:
			a.state = StateConnecting // TCP 已连接但尚未注册，仍显示连接中
			a.errMsg = ""
		case n2n.StateRegistered:
			a.state = StateConnected // 注册成功才是真正连接成功
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

	// 保持 StateConnecting，实际连接是异步的，通过回调更新状态
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

	// 断开连接时移除防火墙规则，不留残留
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
	state := a.state
	a.mu.Unlock()

	// 极速模式：从 Agent 获取真实状态
	if mode == ModeExpress && (state == StateConnected || state == StateConnecting) {
		wgStatus := a.WGGetStatus()
		details := ConnectionDetails{
			Connected:  wgStatus.Connected,
			VirtualIP:  wgStatus.VirtualIP,
			NodeName:   "WireGuard P2P",
			SponsorURL: config.AppSponsorURL,
		}
		switch {
		case wgStatus.Connected:
			details.Status = "正常"
		case state == StateConnecting:
			details.Status = "连接中"
		case state == StateFailed:
			details.Status = "异常"
		default:
			details.Status = "未连接"
		}
		return details
	}

	// 经典模式：原有逻辑
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

// ModeInfo 返回当前模式与可用选项
type ModeInfo struct {
	Current       string `json:"current"`       // classic / express
	AgentRunning  bool   `json:"agentRunning"`  // WireGuard Agent 是否运行中
	DefaultServer string `json:"defaultServer"` // 默认控制服务器地址
	ServerURL     string `json:"serverURL"`     // 用户保存的控制服务器地址
	Nickname      string `json:"nickname"`      // 用户保存的昵称
}

// GetMode 返回当前联机模式及 Agent 状态
func (a *App) GetMode() ModeInfo {
	a.mu.Lock()
	mode := a.cfg.Mode
	if mode == "" {
		mode = string(ModeClassic)
	}
	// agentCmd 仅能检测本程序启动的 Agent；用户可能通过脚本独立启动 Agent（提权），
	// 此时 agentCmd 为 nil 但 Agent 已在运行。通过 HTTP 探测兼顾两种情况。
	agentRunning := (a.agentCmd != nil && a.agentCmd.Process != nil) || isAgentRunning()
	serverURL := normalizeWGServerURL(a.cfg.WGServerURL)
	nickname := a.cfg.WGNickname
	a.mu.Unlock()

	if serverURL == "" {
		serverURL = DefaultWGServerURL
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}

	return ModeInfo{
		Current:       mode,
		AgentRunning:  agentRunning,
		DefaultServer: DefaultWGServerURL,
		ServerURL:     serverURL,
		Nickname:      nickname,
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
		} else {
			_ = a.WGDisconnect()
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

// SaveWGSettings 保存极速模式的配置（服务器地址、昵称）
func (a *App) SaveWGSettings(serverURL, nickname string) error {
	if nickname == "" {
		return fmt.Errorf("昵称不能为空")
	}
	serverURL = normalizeWGServerURL(serverURL)
	if serverURL == "" {
		return fmt.Errorf("服务器地址不能为空")
	}

	a.mu.Lock()
	a.cfg.WGServerURL = serverURL
	a.cfg.WGNickname = nickname
	a.mu.Unlock()

	return config.SaveCached(a.cfg)
}

// ============================================================================
// WireGuard 极速模式 API（封装对 Agent HTTP API 的调用）
// ============================================================================

// WGCreateRoomResponse 创建 WireGuard 房间的响应
type WGCreateRoomResponse struct {
	RoomID     string `json:"room_id"`
	InviteCode string `json:"invite_code"`
	VirtualIP  string `json:"virtual_ip"`
	Subnet     string `json:"subnet"`
}

// WGJoinRoomResponse 加入 WireGuard 房间的响应
type WGJoinRoomResponse struct {
	RoomID    string       `json:"room_id"`
	VirtualIP string       `json:"virtual_ip"`
	Subnet    string       `json:"subnet"`
	Peers     []WGPeerInfo `json:"peers"`
}

// WGPeerInfo WireGuard 节点信息
type WGPeerInfo struct {
	PublicKey string `json:"public_key"`
	VirtualIP string `json:"virtual_ip"`
	Endpoint  string `json:"endpoint"`
	Nickname  string `json:"nickname"`
	Online    bool   `json:"online"`
}

// WGStatusResponse WireGuard 连接状态
type WGStatusResponse struct {
	Connected bool   `json:"connected"`
	PublicKey string `json:"public_key"`
	RoomID    string `json:"room_id"`
	VirtualIP string `json:"virtual_ip"`
	Subnet    string `json:"subnet"`
}

// agentPost 向 Agent 发送 POST 请求
func agentPost(path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("POST", agentBaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

// waitAgentReady 等待 Agent 启动完成（轮询 /api/agent/status）
// 最多等待 5 秒
func waitAgentReady() error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for i := 0; i < 10; i++ {
		resp, err := client.Get(agentBaseURL + "/api/agent/status")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("agent 未就绪，请检查 sogame-agent.exe 是否已启动")
}

// WGCreateRoom 创建 WireGuard 房间（极速模式 - 创建者）
func (a *App) WGCreateRoom(serverURL, nickname string) (*WGCreateRoomResponse, error) {
	serverURL = normalizeWGServerURL(serverURL)
	if serverURL == "" {
		serverURL = DefaultWGServerURL
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}

	// 保存配置
	a.mu.Lock()
	a.cfg.WGServerURL = serverURL
	a.cfg.WGNickname = nickname
	a.state = StateConnecting
	a.errMsg = ""
	a.mu.Unlock()
	_ = config.SaveCached(a.cfg)

	if err := waitAgentReady(); err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = err.Error()
		a.mu.Unlock()
		return nil, err
	}

	resp, err := agentPost("/api/agent/create", map[string]string{
		"server_url": serverURL,
		"nickname":   nickname,
	})
	if err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("连接 Agent 失败: %v", err)
		a.mu.Unlock()
		return nil, fmt.Errorf("连接 Agent 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = errResp.Error
		a.mu.Unlock()
		return nil, fmt.Errorf("%s", errResp.Error)
	}

	var createResp WGCreateRoomResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("解析响应失败: %v", err)
		a.mu.Unlock()
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 保存邀请码
	a.mu.Lock()
	a.cfg.WGInviteCode = createResp.InviteCode
	a.state = StateConnected
	a.errMsg = ""
	a.mu.Unlock()
	_ = config.SaveCached(a.cfg)

	logger.Infof("WG room created: room=%s, ip=%s, invite=%s",
		createResp.RoomID, createResp.VirtualIP, createResp.InviteCode)
	return &createResp, nil
}

// WGJoinRoom 加入 WireGuard 房间（极速模式 - 加入者）
func (a *App) WGJoinRoom(serverURL, inviteCode, nickname string) (*WGJoinRoomResponse, error) {
	serverURL = normalizeWGServerURL(serverURL)
	if serverURL == "" {
		serverURL = DefaultWGServerURL
	}
	if inviteCode == "" {
		return nil, fmt.Errorf("邀请码不能为空")
	}
	if nickname == "" {
		nickname = a.cfg.NodeName
	}

	a.mu.Lock()
	a.cfg.WGServerURL = serverURL
	a.cfg.WGNickname = nickname
	a.cfg.WGInviteCode = inviteCode
	a.state = StateConnecting
	a.errMsg = ""
	a.mu.Unlock()
	_ = config.SaveCached(a.cfg)

	if err := waitAgentReady(); err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = err.Error()
		a.mu.Unlock()
		return nil, err
	}

	resp, err := agentPost("/api/agent/connect", map[string]string{
		"server_url":  serverURL,
		"invite_code": inviteCode,
		"nickname":    nickname,
	})
	if err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("连接 Agent 失败: %v", err)
		a.mu.Unlock()
		return nil, fmt.Errorf("连接 Agent 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = errResp.Error
		a.mu.Unlock()
		return nil, fmt.Errorf("%s", errResp.Error)
	}

	// 先解码为通用 map 获取基础字段
	var raw struct {
		RoomID    string       `json:"room_id"`
		VirtualIP string       `json:"virtual_ip"`
		Subnet    string       `json:"subnet"`
		Peers     []WGPeerInfo `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("解析响应失败: %v", err)
		a.mu.Unlock()
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	a.mu.Lock()
	a.state = StateConnected
	a.errMsg = ""
	a.mu.Unlock()

	logger.Infof("WG room joined: room=%s, ip=%s, peers=%d",
		raw.RoomID, raw.VirtualIP, len(raw.Peers))
	return &WGJoinRoomResponse{
		RoomID:    raw.RoomID,
		VirtualIP: raw.VirtualIP,
		Subnet:    raw.Subnet,
		Peers:     raw.Peers,
	}, nil
}

// WGDisconnect 断开 WireGuard 连接
func (a *App) WGDisconnect() error {
	if err := waitAgentReady(); err != nil {
		// Agent 已退出，直接重置状态
		a.mu.Lock()
		a.state = StateDisconnected
		a.errMsg = ""
		a.mu.Unlock()
		return nil
	}

	resp, err := agentPost("/api/agent/disconnect", map[string]string{})
	if err != nil {
		// Agent 可能在处理断开时崩溃（连接被强制关闭），
		// 此时 WireGuard 接口已随进程退出而移除，直接重置状态即可
		logger.Infof("Agent disconnect request failed (agent may have crashed): %v", err)
		a.mu.Lock()
		a.state = StateDisconnected
		a.errMsg = ""
		a.mu.Unlock()
		return nil
	}
	defer resp.Body.Close()

	a.mu.Lock()
	a.state = StateDisconnected
	a.errMsg = ""
	a.mu.Unlock()
	logger.Infof("WG disconnected")
	return nil
}

// WGGetStatus 获取 WireGuard 连接状态
func (a *App) WGGetStatus() WGStatusResponse {
	if err := waitAgentReady(); err != nil {
		return WGStatusResponse{Connected: false}
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(agentBaseURL + "/api/agent/status")
	if err != nil {
		return WGStatusResponse{Connected: false}
	}
	defer resp.Body.Close()

	var status WGStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&status)
	return status
}

// WGGetInviteCode 返回最近一次创建房间得到的邀请码
func (a *App) WGGetInviteCode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.WGInviteCode
}
