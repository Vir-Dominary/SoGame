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
	"sogame/internal/plugin"
	"sogame/internal/plugins"
)

type AppState string

const (
	StateDisconnected AppState = "disconnected"
	StateConnecting   AppState = "connecting"
	StateConnected    AppState = "connected"
	StateFailed       AppState = "failed"
)

type App struct {
	mu      sync.Mutex
	ctx     context.Context
	edge    *n2n.Edge
	cfg     *config.Config
	state   AppState
	errMsg  string
	plugins *plugin.Manager
	hostIP  string // 房主 VPN IP，从邀请码解析获得（为空表示自己是房主）
}

func NewApp() *App {
	cfg, err := config.LoadOrCreate()
	if err != nil {
		logger.Errorf("failed to load config: %v", err)
		cfg = config.DefaultConfig()
	}

	return &App{
		edge:    &n2n.Edge{},
		cfg:     cfg,
		state:   StateDisconnected,
		plugins: plugin.NewManager(plugins.All()...),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx = ctx
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
	HostIP    string `json:"h"`
}

// communityPrefix 是自动生成的社区名前缀。
// v2 邀请码格式会省略此前缀以缩短长度，解码时再补回。
const communityPrefix = "community-"

// encodeInvite 将邀请数据编码为邀请码。
// 优先使用 v2 紧凑格式（base64url + "|" 分隔），不满足条件时回退到 v1 JSON 格式。
// v2 格式比 v1 短约 40%，通过省略 "community-" 前缀和用 base64url 编码密钥实现。
// v2 格式包含 4 个字段：<community_short>|<key_base64url>|<supernode>|<host_ip>
func encodeInvite(data inviteData) (string, error) {
	// 尝试 v2 格式：仅适用于标准 "community-XXX" 社区名 + hex 密钥
	if strings.HasPrefix(data.Community, communityPrefix) {
		keyBytes, err := hex.DecodeString(data.Key)
		if err == nil {
			communityShort := data.Community[len(communityPrefix):]
			keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)
			inner := communityShort + "|" + keyB64 + "|" + data.Supernode + "|" + data.HostIP
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
// 格式：<community_short>|<key_base64url>|<supernode>[|<host_ip>]
// 第 4 字段 host_ip 为可选（旧版 v2 邀请码不含此字段）。
func parseInviteV2(s string) (*inviteData, error) {
	parts := strings.SplitN(s, "|", 4)
	if len(parts) < 3 {
		return nil, fmt.Errorf("v2 格式字段数不足")
	}

	keyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("v2 密钥解码失败: %w", err)
	}

	data := &inviteData{
		Community: communityPrefix + parts[0],
		Key:       hex.EncodeToString(keyBytes),
		Supernode: parts[2],
	}
	if len(parts) >= 4 {
		data.HostIP = parts[3]
	}
	return data, nil
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

	deviceID := getStableDeviceID()
	hostIP := generateStableIP(deviceID, a.cfg.Community)

	data := inviteData{
		Community: a.cfg.Community,
		Key:       a.cfg.Key,
		Supernode: supernode,
		HostIP:    hostIP,
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
	if data.HostIP != "" {
		logger.Infof("  房主IP: %s", data.HostIP)
	}
	logger.Infof("  分配IP: %s", ip)

	// 保存房主 IP 供插件使用
	a.hostIP = data.HostIP

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
	a.hostIP = ""
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

func (a *App) pluginSession() plugin.Session {
	a.mu.Lock()
	defer a.mu.Unlock()

	return plugin.Session{
		Connected: a.state == StateConnected || a.state == StateConnecting,
		IsHost:    a.hostIP == "" || a.hostIP == a.cfg.IP,
		MyIP:      a.cfg.IP,
		HostIP:    a.hostIP,
		Community: a.cfg.Community,
		Supernode: a.cfg.Supernode,
	}
}

func (a *App) ListPlugins() []plugin.Meta {
	return a.plugins.ListMeta()
}

func (a *App) GetPluginStatus(pluginID string) (plugin.Status, error) {
	return a.plugins.Status(pluginID, a.pluginSession())
}

func (a *App) PluginAction(pluginID, actionID string) error {
	return a.plugins.RunAction(pluginID, actionID, a.pluginSession())
}
