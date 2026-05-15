package n2n

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"netjoin/internal/config"
	"netjoin/internal/logger"
	"netjoin/internal/platform"
)

type StatusCallback func(isRunning bool, message string)

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
	cmd             *exec.Cmd
	mu              sync.Mutex
	done            chan struct{}
	callback        StatusCallback
	isHealthy       bool
	lastHealthCheck time.Time
	config          *config.Config
	autoRestart     bool
	restartCount    int
	maxRestarts     int
	restartCooldown time.Duration
	manualStop      bool
	connectionState ConnectionState
	registeredPeers int
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

func BuildArgs(cfg *config.Config) []string {
	return []string{
		"-c", cfg.Community,
		"-k", cfg.Key,
		"-a", cfg.IP,
		"-l", cfg.Supernode,
	}
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

	if e.cmd != nil && e.cmd.ProcessState == nil {
		e.mu.Unlock()
		return fmt.Errorf("edge process already running (PID: %d)", e.cmd.Process.Pid)
	}

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
	logger.Infof("  Supernode:    %s", cfg.Supernode)
	logger.Infof("  Key:          %s", maskEdgeKey(cfg.Key))
	logger.Infof("  Edge Path:    %s", edgePath)
	logger.Infof("============================================")

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

	var outputBuf bytes.Buffer

	logger.Infof("starting edge process (PID pending)")

	if err := cmd.Start(); err != nil {
		e.connectionState = StateError
		e.mu.Unlock()
		return fmt.Errorf("failed to start edge process: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			outputBuf.WriteString(line + "\n")
			e.parseEdgeOutput(line)
			logger.Debugf("[EDGE] %s", line)
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			outputBuf.WriteString(line + "\n")
			logger.Debugf("[EDGE] %s", line)
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
			logger.Errorf("edge process exited with error: %v", err)

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
						e.callback(false, fmt.Sprintf("进程意外退出，已达到最大重启次数 (%d)", e.maxRestarts))
					} else {
						e.callback(false, "进程意外退出: "+err.Error())
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
			output := outputBuf.String()
			logger.Errorf("edge process exited immediately (PID: %d), output:\n%s", pid, output)
			return fmt.Errorf("edge process exited immediately (PID: %d), output: %s", pid, strings.TrimSpace(output))
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
		err = e.cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("failed to kill edge process: %w", err)
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
			if killErr := e.cmd.Process.Kill(); killErr != nil {
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

	if e.cmd.ProcessState != nil {
		logger.Warnf("edge process health check failed: process already exited")
		e.isHealthy = false
		if e.callback != nil {
			e.callback(false, "进程状态异常: process already exited")
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

func (e *Edge) parseEdgeOutput(line string) {
	lineLower := strings.ToLower(line)

	if strings.Contains(lineLower, "registered with supernode") ||
		strings.Contains(lineLower, "successfully registered") {
		e.mu.Lock()
		e.connectionState = StateRegistered
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已成功注册到中心节点 <<<")
		logger.Infof("    虚拟网络已建立，可以与同群组内其他节点通信")

		go e.postConnectCheck()
	}

	if strings.Contains(lineLower, "connecting to supernode") ||
		strings.Contains(lineLower, "resolving supernode") {
		e.mu.Lock()
		e.connectionState = StateConnecting
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 正在连接中心节点... <<<")
	}

	if strings.Contains(lineLower, "connected to supernode") ||
		strings.Contains(lineLower, "supernode connection established") {
		e.mu.Lock()
		e.connectionState = StateConnected
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已连接到中心节点 <<<")
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
		e.mu.Lock()
		if e.connectionState != StateRegistered {
			e.connectionState = StateError
		}
		e.mu.Unlock()
		logger.Warnf(">>> 连接警告: %s <<<", line)
	}

	if strings.Contains(lineLower, "disconnected") ||
		strings.Contains(lineLower, "connection lost") {
		e.mu.Lock()
		e.connectionState = StateDisconnected
		e.mu.Unlock()
		logger.Warnf(">>> 连接状态: 已断开连接 <<<")
	}
}

func (e *Edge) testSupernodeConnectivity(supernode string) {
	host, port, err := net.SplitHostPort(supernode)
	if err != nil {
		logger.Warnf("中心节点地址格式无效: %s", supernode)
		return
	}

	logger.Infof(">>> 中心节点连通性测试 <<<")
	logger.Infof("  主机: %s", host)
	logger.Infof("  端口: %s", port)

	logger.Infof("  [1/2] DNS 解析测试...")
	ips, err := net.LookupIP(host)
	if err != nil {
		logger.Errorf("  [1/2] DNS 解析失败: %v", err)
	} else {
		logger.Infof("  [1/2] DNS 解析成功: %v", ips)
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
			logger.Infof("  [2/2] UDP 连接成功: 可发送数据到 %s", address)
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
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "0"), 2*time.Second)
	if err != nil {
		return err
	}
	conn.Close()
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
		logger.Infof("  中心节点:     %s", e.config.Supernode)
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
		logger.Infof("  配置 TAP 适配器成功: %s/24, MTU=1290", cfg.IP)
	}

	logger.Infof(">>> TAP 适配器配置完成 <<<")
}
