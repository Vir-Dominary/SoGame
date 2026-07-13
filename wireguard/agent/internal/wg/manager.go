package wg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"sogame/wireguard/agent/internal/logger"
)

// InterfaceName 是 WireGuard 虚拟网卡名称
const InterfaceName = "sogame"

// Peer 表示一个 WireGuard 对等节点
type Peer struct {
	PublicKey   string
	VirtualIP   string
	Endpoint    string
	PresharedKey string
}

// Manager 管理 WireGuard 接口和节点
type Manager struct {
	mu          sync.Mutex
	configDir   string
	privateKey  string
	virtualIP   string
	subnet      string
	peers       map[string]*Peer // public_key -> Peer
	connected   bool
}

// New 创建 WireGuard 管理器
func New(configDir string) *Manager {
	return &Manager{
		configDir: configDir,
		peers:     make(map[string]*Peer),
	}
}

// Connect 创建 WireGuard 接口并配置
func (m *Manager) Connect(privateKey, virtualIP, subnet string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected {
		return fmt.Errorf("already connected")
	}

	m.privateKey = privateKey
	m.virtualIP = virtualIP
	m.subnet = subnet

	// 生成配置文件
	configPath, err := m.generateConfig()
	if err != nil {
		return fmt.Errorf("generate config: %w", err)
	}

	// 安装 WireGuard 隧道服务
	if err := m.installTunnel(configPath); err != nil {
		return fmt.Errorf("install tunnel: %w", err)
	}

	m.connected = true
	logger.Infof("wireguard interface %s created, ip=%s", InterfaceName, virtualIP)
	return nil
}

// Disconnect 移除 WireGuard 接口
func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return nil
	}

	if err := m.uninstallTunnel(); err != nil {
		logger.Warnf("uninstall tunnel: %v", err)
	}

	m.connected = false
	m.peers = make(map[string]*Peer)
	m.privateKey = ""
	m.virtualIP = ""
	m.subnet = ""
	logger.Infof("wireguard interface %s removed", InterfaceName)
	return nil
}

// AddPeer 动态添加一个节点
func (m *Manager) AddPeer(peer *Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return fmt.Errorf("not connected")
	}

	args := []string{
		"set", InterfaceName,
		"peer", peer.PublicKey,
		"allowed-ips", peer.VirtualIP + "/32",
	}
	if peer.Endpoint != "" {
		args = append(args, "endpoint", peer.Endpoint)
	}
	if peer.PresharedKey != "" {
		args = append(args, "preshared-key", peer.PresharedKey)
	}

	if err := runWG(args...); err != nil {
		return fmt.Errorf("wg set peer: %w", err)
	}

	m.peers[peer.PublicKey] = peer
	logger.Infof("peer added: %s -> %s", maskKey(peer.PublicKey), peer.VirtualIP)
	return nil
}

// RemovePeer 动态移除一个节点
func (m *Manager) RemovePeer(publicKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return fmt.Errorf("not connected")
	}

	if err := runWG(
		"set", InterfaceName,
		"peer", publicKey,
		"remove",
	); err != nil {
		return fmt.Errorf("wg remove peer: %w", err)
	}

	delete(m.peers, publicKey)
	logger.Infof("peer removed: %s", maskKey(publicKey))
	return nil
}

// GetPeers 返回当前所有节点
func (m *Manager) GetPeers() []*Peer {
	m.mu.Lock()
	defer m.mu.Unlock()

	peers := make([]*Peer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	return peers
}

// IsConnected 返回是否已连接
func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// GetStatus 返回当前状态
func (m *Manager) GetStatus() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	return map[string]interface{}{
		"connected":  m.connected,
		"interface":  InterfaceName,
		"virtual_ip": m.virtualIP,
		"subnet":     m.subnet,
		"peer_count": len(m.peers),
	}
}

// generateConfig 生成 WireGuard 配置文件
func (m *Manager) generateConfig() (string, error) {
	configContent := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/24
ListenPort = 51820
`, m.privateKey, m.virtualIP)

	configPath := filepath.Join(m.configDir, InterfaceName+".conf")
	if err := os.MkdirAll(m.configDir, 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		return "", err
	}
	return configPath, nil
}

// installTunnel 安装 WireGuard 隧道服务（Windows）
func (m *Manager) installTunnel(configPath string) error {
	// Windows: wireguard.exe /installtunnel <config>
	cmd := exec.Command("wireguard.exe", "/installtunnel", configPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installtunnel: %w: %s", err, string(output))
	}
	return nil
}

// uninstallTunnel 卸载 WireGuard 隧道服务（Windows）
func (m *Manager) uninstallTunnel() error {
	// Windows: wireguard.exe /uninstalltunnel <name>
	cmd := exec.Command("wireguard.exe", "/uninstalltunnel", InterfaceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("uninstalltunnel: %w: %s", err, string(output))
	}
	return nil
}

// runWG 执行 wg 命令
func runWG(args ...string) error {
	cmd := exec.Command("wg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return nil
}

// maskKey 脱敏公钥
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}
