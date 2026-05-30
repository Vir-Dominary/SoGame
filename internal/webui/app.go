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
	"sync"

	"netjoin/internal/config"
	"netjoin/internal/logger"
	"netjoin/internal/n2n"
	"netjoin/internal/platform"
	"netjoin/internal/plugin"
	"netjoin/internal/plugins"
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
	n2n.KillOrphanEdgeProcess()
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

func (a *App) GetNodes() []NodeInfo {
	return []NodeInfo{
		{Name: "公用节点——中国成都", Address: "119.6.178.183:10090"},
		{Name: "公用节点——英国", Address: "146.56.108.91:10090"},
		{Name: "公用节点——中国中山", Address: "116.28.76.77:10090"},
		{Name: "公用节点——韩国", Address: "[2603:c024:5:5f5f:203d:234:6c3d:593c]:10090"},
		{Name: "临时节点——中国北京", Address: "117.72.86.224:10090"},
		{Name: "临时节点——中国深圳", Address: "8.148.244.159:10090"},
		{Name: "临时节点——中国河北", Address: "111.225.98.22:10090"},
		{Name: "临时节点——中国苏州", Address: "n2n.vvcd.win:10090"},
	}
}

type inviteData struct {
	Community string `json:"c"`
	Key       string `json:"k"`
	Supernode string `json:"s"`
	HostIP    string `json:"h"`
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
	b, _ := hex.DecodeString(hash[:2])
	host := b[0]%254 + 1
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

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("生成邀请码失败: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(jsonBytes)
	return encoded, nil
}

func (a *App) ConnectWithInvite(code string) error {
	decoded, err := base64.StdEncoding.DecodeString(code)
	if err != nil {
		return fmt.Errorf("无效的邀请码格式: %w", err)
	}

	var data inviteData
	if err := json.Unmarshal(decoded, &data); err != nil {
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
		return fmt.Errorf(a.errMsg)
	}

	if a.cfg.Key == "" {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = "请先设置密码"
		a.mu.Unlock()
		return fmt.Errorf(a.errMsg)
	}

	if !platform.IsSoGameAdapterExists() {
		status, err := platform.EnsureSoGameAdapter()
		if err != nil || (status != platform.TapInstallSuccess && status != platform.TapAlreadyInstalled) {
			a.mu.Lock()
			a.state = StateFailed
			a.errMsg = fmt.Sprintf("网络适配器安装失败: %v", err)
			a.mu.Unlock()
			return fmt.Errorf(a.errMsg)
		}
	} else {
		platform.EnableTapInterface(platform.SoGameAdapterName)
	}

	// 在启动 edge 之前设置回调，因为 edge 可能在 Start() 返回前就输出注册成功
	a.edge.SetConnectionStateCallback(func(state n2n.ConnectionState) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch state {
		case n2n.StateRegistered:
			a.state = StateConnected
			a.errMsg = ""
		case n2n.StateError:
			a.state = StateFailed
			a.errMsg = "连接过程中发生错误"
		case n2n.StateDisconnected:
			a.state = StateDisconnected
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

	err := a.edge.Start(a.cfg)
	if err != nil {
		a.mu.Lock()
		a.state = StateFailed
		a.errMsg = fmt.Sprintf("连接失败: %v", err)
		a.mu.Unlock()
		return fmt.Errorf(a.errMsg)
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
	AppName     string `json:"appName"`
	AppVersion  string `json:"appVersion"`
	AppAuthor   string `json:"appAuthor"`
	AppURL      string `json:"appURL"`
	AppBilibili string `json:"bilibiliURL"`
	AppDesc     string `json:"appDesc"`
}

func (a *App) GetAboutInfo() AboutInfo {
	return AboutInfo{
		AppName:     config.AppName,
		AppVersion:  config.AppVersion,
		AppAuthor:   config.AppAuthor,
		AppURL:      config.AppURL,
		AppBilibili: config.AppBilibili,
		AppDesc:     config.AppDesc,
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
