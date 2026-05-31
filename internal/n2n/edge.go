package n2n

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sogame/internal/config"
	"sogame/internal/logger"
	"sogame/internal/platform"
)

type StatusCallback func(isRunning bool, message string)
type ConnectionStateCallback func(state ConnectionState)

type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateRegistering
	StateRegistered
	StateError
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "已断开"
	case StateConnecting:
		return "连接中"
	case StateConnected:
		return "已连接"
	case StateRegistering:
		return "注册中"
	case StateRegistered:
		return "已注册"
	case StateError:
		return "错误"
	default:
		return "未知"
	}
}

type Edge struct {
	cmd                     *exec.Cmd
	mu                      sync.Mutex
	done                    chan struct{}
	callback                StatusCallback
	connectionStateCallback ConnectionStateCallback
	isHealthy               bool
	lastHealthCheck         time.Time
	config                  *config.Config
	autoRestart             bool
	restartCount            int
	maxRestarts             int
	restartCooldown         time.Duration
	manualStop              bool
	connectionState         ConnectionState
	registeredPeers         int
}

func maskEdgeKey(key string) string {
	if key == "" {
		return "(none)"
	}
	if len(key) <= 4 {
		return "****"
	}
	return key[:2] + strings.Repeat("*", len(key)-4) + key[len(key)-2:]
}

// MaskSupernode 脱敏中心节点地址，仅显示节点名称
func MaskSupernode(address string) string {
	name := lookupNodeName(address)
	if name != "" {
		return name
	}
	// 未知节点，脱敏显示：隐藏 IP 中间部分
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "***"
	}
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		return parts[0] + ".***.***." + parts[3] + ":" + port
	}
	// 主机名或 IPv6，仅显示首尾
	if len(host) > 8 {
		return host[:4] + "***" + host[len(host)-2:] + ":" + port
	}
	return "***:" + port
}

// knownNodes 已知的公用节点列表（地址 -> 名称）
var knownNodes = map[string]string{
	"119.6.178.183:10090":                         "公用节点——中国成都",
	"146.56.108.91:10090":                         "公用节点——英国",
	"116.28.76.77:10090":                          "公用节点——中国中山",
	"[2603:c024:5:5f5f:203d:234:6c3d:593c]:10090": "公用节点——韩国",
	"117.72.86.224:10090":                         "临时节点——中国北京",
	"8.148.244.159:10090":                         "临时节点——中国深圳",
	"111.225.98.22:10090":                         "临时节点——中国河北",
	"n2n.vvcd.win:10090":                          "临时节点——中国苏州",
}

func lookupNodeName(address string) string {
	return LookupNodeName(address)
}

func LookupNodeName(address string) string {
	return knownNodes[address]
}

type KnownNode struct {
	Name    string
	Address string
}

func GetKnownNodes() []KnownNode {
	nodes := make([]KnownNode, 0, len(knownNodes))
	for addr, name := range knownNodes {
		nodes = append(nodes, KnownNode{Name: name, Address: addr})
	}
	return nodes
}

func BuildArgs(cfg *config.Config) []string {
	args := []string{
		"-c", cfg.Community,
		"-k", cfg.Key,
		"-a", cfg.IP,
		"-l", cfg.Supernode,
		"-r",
		"-v",
	}

	// 指定使用 SoGame 专属 TAP 适配器
	if platform.IsSoGameAdapterExists() {
		args = append(args, "-d", platform.SoGameAdapterName)
	}

	return args
}

func BuildArgsForLogging(cfg *config.Config) []string {
	return []string{
		"-c", cfg.Community,
		"-k", "******",
		"-a", cfg.IP,
		"-l", cfg.Supernode,
	}
}

func (e *Edge) Start(cfg *config.Config) error {
	e.mu.Lock()

	// 如果已有 edge 进程在运行，先停止它再重新启动
	if e.cmd != nil && e.cmd.ProcessState == nil {
		pid := e.cmd.Process.Pid
		e.mu.Unlock()
		logger.Warnf("edge process already running (PID: %d), stopping it before restart", pid)
		if err := e.Stop(); err != nil {
			logger.Warnf("failed to stop existing edge process: %v", err)
		}
		e.Reset()
		e.mu.Lock()
	}

	// 清理系统中可能残留的孤儿 edge 进程
	e.mu.Unlock()
	KillOrphanEdgeProcess()
	e.mu.Lock()

	e.manualStop = false
	e.connectionState = StateConnecting
	e.registeredPeers = 0
	e.config = cfg

	if e.maxRestarts == 0 {
		e.maxRestarts = 3
	}
	if e.restartCooldown == 0 {
		e.restartCooldown = 10 * time.Second
	}

	edgePath, err := findEdgeExecutable()
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("failed to locate edge.exe: %w", err)
	}

	logger.Infof("========== EDGE CONNECTION START ==========")
	logger.Infof("  Community:    %s", cfg.Community)
	logger.Infof("  Local IP:     %s", cfg.IP)
	logger.Infof("  Supernode:    %s", MaskSupernode(cfg.Supernode))
	logger.Infof("  Key:          %s", maskEdgeKey(cfg.Key))
	logger.Infof("============================================")

	// 在启动 edge 之前，确保 TAP 适配器处于启用状态
	// 其他 VPN 软件（UU 加速器、Redmin VPN 等）关闭时可能会禁用 TAP 适配器
	if tapName := platform.FindTapInterfaceName(); tapName != "" {
		platform.EnableTapInterface(tapName)
	}

	go e.testSupernodeConnectivity(cfg.Supernode)

	args := BuildArgs(cfg)

	logger.Debugf("edge args:")
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			logger.Debugf("  %s %s", args[i], args[i+1])
		} else {
			logger.Debugf("  %s", args[i])
		}
	}

	cmd := exec.Command(edgePath, args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		e.mu.Unlock()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	logger.Infof("starting edge process (PID pending)")

	if err := cmd.Start(); err != nil {
		e.connectionState = StateError
		e.mu.Unlock()
		return fmt.Errorf("failed to start edge process: %w", err)
	}

	// 保存 PID 到文件，用于清理孤儿进程
	saveEdgePID(cmd.Process.Pid)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			e.parseEdgeOutput(line)
			logger.Debugf("[EDGE stdout] %s", line)
		}
	}()

	// 收集 stderr 输出，用于诊断 edge 进程启动失败
	var stderrBuf strings.Builder
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuf.WriteString(line)
			stderrBuf.WriteString("\n")
			logger.Infof("[EDGE stderr] %s", line)
		}
	}()

	e.cmd = cmd
	e.done = make(chan struct{})
	e.isHealthy = true
	e.lastHealthCheck = time.Now()

	done := e.done
	pid := e.cmd.Process.Pid

	e.mu.Unlock()

	go func() {
		err := e.cmd.Wait()
		close(done)

		// 进程退出后清除 PID 文件
		clearEdgePID()

		e.mu.Lock()
		e.isHealthy = false
		wasManualStop := e.manualStop
		shouldRestart := e.autoRestart && e.restartCount < e.maxRestarts && !wasManualStop
		e.mu.Unlock()

		if wasManualStop {
			logger.Infof("edge process stopped by user (PID: %d)", pid)
			return
		}

		if err != nil {
			stderrOutput := strings.TrimSpace(stderrBuf.String())
			logger.Errorf("edge process exited with error: %v", err)
			if stderrOutput != "" {
				logger.Errorf("edge stderr output:\n%s", stderrOutput)
			}

			if shouldRestart {
				e.mu.Lock()
				e.restartCount++
				e.mu.Unlock()

				logger.Infof("attempting auto-restart (%d/%d) after %v",
					e.restartCount, e.maxRestarts, e.restartCooldown)

				time.Sleep(e.restartCooldown)

				if restartErr := e.Start(cfg); restartErr != nil {
					logger.Errorf("auto-restart failed: %v", restartErr)
					if e.callback != nil {
						e.callback(false, "进程意外退出，自动重启失败: "+restartErr.Error())
					}
				} else {
					logger.Infof("auto-restart succeeded")
					if e.callback != nil {
						e.callback(true, "进程已自动重启")
					}
				}
			} else {
				if e.callback != nil {
					if e.restartCount >= e.maxRestarts {
						stderrOutput := strings.TrimSpace(stderrBuf.String())
						errMsg := fmt.Sprintf("进程意外退出，已达到最大重启次数 (%d)", e.maxRestarts)
						if stderrOutput != "" {
							errMsg += "\n错误输出:\n" + stderrOutput
						}
						e.callback(false, errMsg)
					} else {
						stderrOutput := strings.TrimSpace(stderrBuf.String())
						errMsg := "进程意外退出: " + err.Error()
						if stderrOutput != "" {
							errMsg += "\n错误输出:\n" + stderrOutput
						}
						e.callback(false, errMsg)
					}
				}
			}
		} else {
			logger.Infof("edge process exited normally (PID: %d)", pid)
			if e.callback != nil {
				e.callback(false, "进程已停止")
			}
		}
	}()

	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		e.mu.Lock()
		exited := e.cmd.ProcessState != nil
		e.mu.Unlock()
		if exited {
			// 等待 stderr 收集完成
			time.Sleep(200 * time.Millisecond)
			stderrOutput := strings.TrimSpace(stderrBuf.String())
			logger.Errorf("edge process exited immediately (PID: %d)", pid)
			if stderrOutput != "" {
				logger.Errorf("edge stderr output:\n%s", stderrOutput)
				return fmt.Errorf("edge 进程立即退出 (PID: %d)\n错误输出:\n%s", pid, stderrOutput)
			}
			return fmt.Errorf("edge 进程立即退出 (PID: %d)，无错误输出", pid)
		}
	}

	go e.startHealthCheck()

	go e.configureTapInterface(cfg)

	logger.Infof("edge process started successfully (PID: %d)", pid)
	return nil
}

func (e *Edge) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cmd = nil
	e.done = nil
	e.isHealthy = false
	e.connectionState = StateDisconnected
	e.registeredPeers = 0
	e.manualStop = false
	e.restartCount = 0
}

func (e *Edge) Stop() error {
	e.mu.Lock()
	if e.cmd == nil || e.cmd.ProcessState != nil {
		e.mu.Unlock()
		return nil
	}

	e.manualStop = true
	pid := e.cmd.Process.Pid
	done := e.done
	e.mu.Unlock()

	logger.Infof("stopping edge process (PID: %d)", pid)

	err := e.cmd.Process.Signal(os.Interrupt)
	if err != nil {
		logger.Warnf("failed to send interrupt signal: %v, attempting force kill", err)
		killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
		killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if killErr := killCmd.Run(); killErr != nil {
			return fmt.Errorf("failed to kill edge process: %w", killErr)
		}
	}

	select {
	case <-done:
		logger.Infof("edge process terminated successfully (PID: %d)", pid)
	case <-time.After(5 * time.Second):
		e.mu.Lock()
		stillRunning := e.cmd.ProcessState == nil
		e.mu.Unlock()
		if stillRunning {
			logger.Warnf("graceful shutdown timeout, force killing process (PID: %d)", pid)
			killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
			killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if killErr := killCmd.Run(); killErr != nil {
				logger.Errorf("failed to force kill edge process (PID: %d): %v", pid, killErr)
			}
		}
		select {
		case <-done:
			logger.Infof("edge process force terminated (PID: %d)", pid)
		case <-time.After(2 * time.Second):
			return fmt.Errorf("edge process did not terminate within 7 seconds (PID: %d)", pid)
		}
	}

	return nil
}

func (e *Edge) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cmd != nil && e.cmd.ProcessState == nil
}

func (e *Edge) SetStatusCallback(callback StatusCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.callback = callback
}

func (e *Edge) SetConnectionStateCallback(callback ConnectionStateCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.connectionStateCallback = callback
}

func (e *Edge) GetStatus() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cmd == nil {
		return "未初始化"
	}

	if e.cmd.ProcessState == nil {
		if e.isHealthy {
			return fmt.Sprintf("运行中 (PID: %d) - 健康", e.cmd.Process.Pid)
		} else {
			return fmt.Sprintf("运行中 (PID: %d) - 异常", e.cmd.Process.Pid)
		}
	}

	if e.cmd.ProcessState.Success() {
		return "已停止（正常）"
	}

	return fmt.Sprintf("已停止（异常: %s）", e.cmd.ProcessState.String())
}

func (e *Edge) startHealthCheck() {
	ticker := time.NewTicker(10 * time.Second)
	statusTicker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer statusTicker.Stop()

	for {
		select {
		case <-ticker.C:
			e.checkHealth()
		case <-statusTicker.C:
			e.LogConnectionStatus()
		case <-e.done:
			return
		}
	}
}

func (e *Edge) checkHealth() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cmd == nil || e.cmd.ProcessState != nil {
		return
	}

	if e.cmd.Process == nil {
		logger.Warnf("edge process health check failed: process is nil")
		e.isHealthy = false
		if e.callback != nil {
			e.callback(false, "进程状态异常: process is nil")
		}
		return
	}

	e.isHealthy = true
	e.lastHealthCheck = time.Now()
}

func (e *Edge) IsHealthy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isHealthy
}

func (e *Edge) SetAutoRestart(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.autoRestart = enabled
}

func (e *Edge) SetMaxRestarts(max int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.maxRestarts = max
}

func (e *Edge) SetRestartCooldown(duration time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.restartCooldown = duration
}

func (e *Edge) GetRestartCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.restartCount
}

func (e *Edge) ResetRestartCount() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.restartCount = 0
}

func findEdgeExecutable() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	baseDir := filepath.Dir(exePath)

	candidates := []string{
		filepath.Join(baseDir, "edge.exe"),
		filepath.Join(baseDir, "bin", "edge.exe"),
		// wails build 输出到 build/bin/，但 edge.exe 可能在项目根的 bin/ 下
		filepath.Join(baseDir, "..", "bin", "edge.exe"),
		filepath.Join(baseDir, "..", "..", "bin", "edge.exe"),
	}

	for _, path := range candidates {
		absPath, _ := filepath.Abs(path)
		if _, err := os.Stat(absPath); err == nil {
			logger.Debugf("found edge.exe at: %s", absPath)
			return absPath, nil
		}
	}

	return "", fmt.Errorf(
		"edge.exe not found in searched paths: %v. executable dir: %s. ensure edge.exe is in the same directory or in bin/ subdirectory",
		candidates,
		baseDir,
	)
}

// edgePIDPath 返回 edge 进程 PID 文件路径
func edgePIDPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SoGame", "edge.pid"), nil
}

// saveEdgePID 保存 edge 进程 PID 到文件
func saveEdgePID(pid int) {
	path, err := edgePIDPath()
	if err != nil {
		logger.Warnf("failed to get edge PID file path: %v", err)
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0600); err != nil {
		logger.Warnf("failed to save edge PID: %v", err)
	}
}

// clearEdgePID 清除 edge 进程 PID 文件
func clearEdgePID() {
	path, err := edgePIDPath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

// KillOrphanEdgeProcess 通过 PID 文件清理上次运行遗留的 edge 进程
func KillOrphanEdgeProcess() {
	path, err := edgePIDPath()
	if err != nil {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return // PID 文件不存在，无需清理
	}

	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		_ = os.Remove(path)
		return
	}

	// 检查进程是否仍在运行
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(path)
		return
	}

	// 尝试发送信号检查进程是否存在（Windows 上 FindProcess 总是成功）
	if err := proc.Signal(os.Interrupt); err != nil {
		// 进程不存在，清除 PID 文件
		_ = os.Remove(path)
		return
	}

	logger.Infof("found orphan edge process (PID: %d), terminating...", pid)
	// 使用 taskkill 命令而非 proc.Kill()，避免被杀毒软件标记为可疑进程操作
	killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := killCmd.Run(); err != nil {
		logger.Warnf("failed to kill orphan edge process (PID: %d): %v", pid, err)
	} else {
		logger.Infof("orphan edge process terminated (PID: %d)", pid)
	}

	_ = os.Remove(path)
}

func (e *Edge) parseEdgeOutput(line string) {
	lineLower := strings.ToLower(line)

	if strings.Contains(lineLower, "registered with supernode") ||
		strings.Contains(lineLower, "successfully registered") ||
		(strings.Contains(lineLower, "<<<") && strings.Contains(lineLower, ">>>") && strings.Contains(lineLower, "supernode")) {
		e.mu.Lock()
		if e.connectionState == StateRegistered {
			e.mu.Unlock()
			return
		}
		e.connectionState = StateRegistered
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已成功注册到中心节点 <<<")
		logger.Infof("    虚拟网络已建立，可以与同群组内其他节点通信")

		if cb != nil {
			cb(StateRegistered)
		}

		go e.postConnectCheck()
		return
	}

	if strings.Contains(lineLower, "connecting to supernode") ||
		strings.Contains(lineLower, "resolving supernode") {
		e.mu.Lock()
		e.connectionState = StateConnecting
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 正在连接中心节点... <<<")

		if cb != nil {
			cb(StateConnecting)
		}
	}

	if strings.Contains(lineLower, "connected to supernode") ||
		strings.Contains(lineLower, "supernode connection established") {
		e.mu.Lock()
		e.connectionState = StateConnected
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已连接到中心节点 <<<")

		if cb != nil {
			cb(StateConnected)
		}
	}

	if strings.Contains(lineLower, "peer") && strings.Contains(lineLower, "added") {
		e.mu.Lock()
		e.registeredPeers++
		peers := e.registeredPeers
		e.mu.Unlock()
		logger.Infof(">>> 节点发现: 发现新节点 (当前群内共 %d 个节点) <<<", peers)
	}

	if strings.Contains(lineLower, "error") ||
		strings.Contains(lineLower, "failed") ||
		strings.Contains(lineLower, "cannot") {
		logger.Warnf(">>> 连接警告: %s <<<", line)

		e.mu.Lock()
		if e.connectionState != StateRegistered && e.connectionState != StateConnected {
			e.connectionState = StateError
			cb := e.connectionStateCallback
			e.mu.Unlock()

			if cb != nil {
				cb(StateError)
			}
		} else {
			e.mu.Unlock()
		}
	}

	if strings.Contains(lineLower, "disconnected") ||
		strings.Contains(lineLower, "connection lost") {
		e.mu.Lock()
		e.connectionState = StateDisconnected
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Warnf(">>> 连接状态: 已断开连接 <<<")

		if cb != nil {
			cb(StateDisconnected)
		}
	}
}

func (e *Edge) testSupernodeConnectivity(supernode string) {
	host, port, err := net.SplitHostPort(supernode)
	if err != nil {
		logger.Warnf("中心节点地址格式无效")
		return
	}

	logger.Infof(">>> 中心节点连通性测试 <<<")
	logger.Infof("  节点: %s", MaskSupernode(supernode))

	logger.Infof("  [1/2] DNS 解析测试...")
	_, err = net.LookupIP(host)
	if err != nil {
		logger.Errorf("  [1/2] DNS 解析失败: %v", err)
	} else {
		logger.Infof("  [1/2] DNS 解析成功")
	}

	logger.Infof("  [2/2] UDP 连接测试...")
	address := net.JoinHostPort(host, port)
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		logger.Errorf("  [2/2] UDP 解析失败: %v", err)
	} else {
		udpConn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			logger.Errorf("  [2/2] UDP 连接失败: %v", err)
		} else {
			udpConn.Close()
			logger.Infof("  [2/2] UDP 连接成功")
		}
	}

	logger.Infof(">>> 连通性测试完成 <<<")
}

func (e *Edge) postConnectCheck() {
	time.Sleep(3 * time.Second)

	e.mu.Lock()
	cfg := e.config
	e.mu.Unlock()

	if cfg == nil {
		return
	}

	logger.Infof(">>> 连接后网络检测 <<<")

	pingTarget := cfg.IP
	logger.Infof("  VPN 内部连通性测试 (%s)...", pingTarget)
	if err := e.pingVPNAddress(pingTarget); err != nil {
		logger.Warnf("  VPN 内部连通性测试失败: %v (可能群内没有其他节点在线)", err)
	} else {
		logger.Infof("  VPN 内部连通性测试成功 ✓")
	}

	logger.Infof(">>> 网络检测完成 <<<")
}

func (e *Edge) pingVPNAddress(ip string) error {
	cmd := exec.Command("ping", "-n", "1", "-w", "2000", ip)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ping %s failed: %v", ip, err)
	}
	_ = output
	return nil
}

func (e *Edge) GetConnectionState() ConnectionState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.connectionState
}

func (e *Edge) GetRegisteredPeers() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.registeredPeers
}

func (e *Edge) LogConnectionStatus() {
	e.mu.Lock()
	defer e.mu.Unlock()

	var processStatus string
	if e.cmd == nil {
		processStatus = "未初始化"
	} else if e.cmd.ProcessState == nil {
		if e.isHealthy {
			processStatus = fmt.Sprintf("运行中 (PID: %d) - 健康", e.cmd.Process.Pid)
		} else {
			processStatus = fmt.Sprintf("运行中 (PID: %d) - 异常", e.cmd.Process.Pid)
		}
	} else if e.cmd.ProcessState.Success() {
		processStatus = "已停止（正常）"
	} else {
		processStatus = fmt.Sprintf("已停止（异常: %s）", e.cmd.ProcessState.String())
	}

	logger.Infof("========== 连接状态 ==========")
	logger.Infof("  状态:         %s", e.connectionState.String())
	logger.Infof("  进程:         %s", processStatus)
	logger.Infof("  已注册:       %s", func() string {
		if e.isHealthy {
			return "是"
		}
		return "否"
	}())
	logger.Infof("  群内节点数:   %d", e.registeredPeers)
	if e.config != nil {
		logger.Infof("  中心节点:     %s", MaskSupernode(e.config.Supernode))
		logger.Infof("  群名:         %s", e.config.Community)
		logger.Infof("  本机IP:       %s", e.config.IP)
	}
	logger.Infof("=======================================")
}

func (e *Edge) configureTapInterface(cfg *config.Config) {
	time.Sleep(2 * time.Second)

	e.mu.Lock()
	if e.cmd == nil || e.cmd.ProcessState != nil {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	ifName := platform.FindTapInterfaceName()
	if ifName == "" {
		logger.Warnf("未能找到 TAP 适配器接口名，跳过 IP 配置")
		return
	}

	logger.Infof(">>> 配置 TAP 适配器 <<<")
	logger.Infof("  接口名: %s", ifName)
	logger.Infof("  IP 地址: %s", cfg.IP)

	if err := platform.ConfigureTapInterface(ifName, cfg.IP); err != nil {
		logger.Errorf("  配置 TAP 适配器失败: %v", err)
	} else {
		logger.Infof("  配置 TAP 适配器成功: %s/16, MTU=1290", cfg.IP)
	}

	logger.Infof(">>> TAP 适配器配置完成 <<<")
}
