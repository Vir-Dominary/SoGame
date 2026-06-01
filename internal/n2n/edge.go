package n2n

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"netjoin/internal/config"
	"netjoin/internal/logger"
	"netjoin/internal/platform"
)

type StatusCallback func(isRunning bool, message string)
type ConnectionStateCallback func(state ConnectionState)

type ConnectionState int

const (
	authRetryDelay         = 5 * time.Second
	maxAuthConflictRetry   = 2
	newProcessGroupFlag    = 0x00000200

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
	stopMu                  sync.Mutex
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
	authConflictRetries     int
	registrationRetryPending bool
	mgmtPort                int
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
	return knownNodes[address]
}

func BuildArgs(cfg *config.Config, mgmtPort int) []string {
	args := []string{
		"-c", cfg.Community,
		"-k", cfg.Key,
		"-a", cfg.IP,
		"-l", cfg.Supernode,
		"-r",
		"-v",
	}

	// 开放管理端口，用于优雅退出
	if mgmtPort > 0 {
		args = append(args, "-t", strconv.Itoa(mgmtPort))
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
	e.stopMu.Lock()
	e.stopMu.Unlock()

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
	e.authConflictRetries = 0
	e.registrationRetryPending = false
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

	// TAP 适配器已由 app.Connect() 完成安装和启用，此处不再重复操作

	go e.testSupernodeConnectivity(cfg.Supernode)

	mgmtPort, err := allocateUDPPort()
	if err != nil {
		logger.Warnf("failed to allocate management port, graceful shutdown unavailable: %v", err)
	}
	e.mgmtPort = mgmtPort

	args := BuildArgs(cfg, mgmtPort)

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
		HideWindow:    true,
		CreationFlags: newProcessGroupFlag,
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
	e.authConflictRetries = 0
	e.registrationRetryPending = false
	e.mgmtPort = 0
}

func (e *Edge) Stop() error {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()

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

	if err := terminateEdgeProcess(pid, done, e.mgmtPort); err != nil {
		return err
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

// KillOrphanEdgeProcess 通过 PID 文件清理上次运行遗留的 edge 进程。
// 返回值表示是否终止了仍在运行的进程。
func KillOrphanEdgeProcess() bool {
	path, err := edgePIDPath()
	if err != nil {
		return false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return false
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		_ = os.Remove(path)
		return false
	}

	if !edgeProcessRunning(pid) {
		_ = os.Remove(path)
		return false
	}

	logger.Infof("found orphan edge process (PID: %d), terminating...", pid)
	if err := terminateEdgeProcess(pid, nil, 0); err != nil {
		logger.Warnf("failed to kill orphan edge process (PID: %d): %v", pid, err)
	} else {
		logger.Infof("orphan edge process terminated (PID: %d)", pid)
	}

	_ = os.Remove(path)
	return true
}

func allocateUDPPort() (int, error) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return 0, err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port, nil
}

func sendMgmtStop(port int) bool {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write([]byte("stop\n"))
	return err == nil
}

func terminateEdgeProcess(pid int, done <-chan struct{}, mgmtPort int) error {
	if runtime.GOOS == "windows" {
		return terminateEdgeProcessWindows(pid, done, mgmtPort)
	}
	return terminateEdgeProcessUnix(pid, done)
}

func terminateEdgeProcessUnix(pid int, done <-chan struct{}) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		logger.Warnf("failed to send interrupt signal: %v, attempting kill", err)
		if killErr := proc.Kill(); killErr != nil {
			return fmt.Errorf("failed to kill edge process: %w", killErr)
		}
	}
	return waitForProcessExit(pid, done, 7*time.Second)
}

func terminateEdgeProcessWindows(pid int, done <-chan struct{}, mgmtPort int) error {
	if mgmtPort > 0 {
		logger.Debugf("sending stop command to edge management port %d (PID: %d)", mgmtPort, pid)
		if sendMgmtStop(mgmtPort) {
			if waitForProcessExit(pid, done, 8*time.Second) == nil {
				logger.Infof("edge process terminated gracefully via management port (PID: %d)", pid)
				return nil
			}
			logger.Warnf("graceful shutdown timeout after management stop (PID: %d)", pid)
		} else {
			logger.Debugf("management stop failed, trying taskkill without /F (PID: %d)", pid)
		}
	}

	if err := runTaskkill(pid, false); err != nil {
		logger.Debugf("taskkill without /F failed: %v", err)
	} else if waitForProcessExit(pid, done, 5*time.Second) == nil {
		logger.Infof("edge process terminated via taskkill (PID: %d)", pid)
		return nil
	}

	logger.Warnf("force killing edge process (PID: %d)", pid)
	if err := runTaskkill(pid, true); err != nil {
		return fmt.Errorf("failed to force kill edge process: %w", err)
	}
	if err := waitForProcessExit(pid, done, 3*time.Second); err != nil {
		return err
	}
	logger.Infof("edge process force terminated (PID: %d)", pid)
	return nil
}

func runTaskkill(pid int, force bool) error {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	killCmd := exec.Command("taskkill", args...)
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return killCmd.Run()
}

func waitForProcessExit(pid int, done <-chan struct{}, timeout time.Duration) error {
	if done != nil {
		select {
		case <-done:
			return nil
		case <-time.After(timeout):
		}
	} else {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if !edgeProcessRunning(pid) {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !edgeProcessRunning(pid) {
		return nil
	}
	return fmt.Errorf("edge process did not terminate within %v (PID: %d)", timeout, pid)
}

func edgeProcessRunning(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").
		CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

func (e *Edge) scheduleRegistrationRetry() {
	e.mu.Lock()
	if e.manualStop || e.registrationRetryPending {
		e.mu.Unlock()
		return
	}
	if e.authConflictRetries >= maxAuthConflictRetry {
		e.mu.Unlock()
		return
	}
	e.registrationRetryPending = true
	e.authConflictRetries++
	retryNum := e.authConflictRetries
	cfg := e.config
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			e.registrationRetryPending = false
			e.mu.Unlock()
		}()

		delay := authRetryDelay * time.Duration(retryNum)
		logger.Infof("supernode 尚未释放上次注册，%v 后自动重试 (%d/%d)...",
			delay, retryNum, maxAuthConflictRetry)
		time.Sleep(delay)

		e.mu.Lock()
		manual := e.manualStop
		e.mu.Unlock()
		if manual || cfg == nil {
			return
		}

		if err := e.Stop(); err != nil {
			logger.Warnf("registration retry: stop failed: %v", err)
		}
		e.Reset()
		if err := e.Start(cfg); err != nil {
			logger.Errorf("registration retry: start failed: %v", err)
			if e.callback != nil {
				e.callback(false, "注册冲突，自动重试失败: "+err.Error())
			}
		}
	}()
}

func (e *Edge) parseEdgeOutput(line string) {
	lineLower := strings.ToLower(line)

	if strings.Contains(lineLower, "registered with supernode") ||
		strings.Contains(lineLower, "successfully registered") {
		e.mu.Lock()
		e.connectionState = StateRegistered
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已成功注册到中心节点 <<<")
		logger.Infof("    虚拟网络已建立，可以与同群组内其他节点通信")

		if cb != nil {
			cb(StateRegistered)
		}

		go e.postConnectCheck()
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

	// 捕获 edge 的 <<< ================ >>> supernode 标记行
	// 这是 n2n edge 成功连接到 supernode 的标志
	if strings.Contains(lineLower, "<<<") && strings.Contains(lineLower, ">>>") && strings.Contains(lineLower, "supernode") {
		e.mu.Lock()
		e.connectionState = StateRegistered
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof(">>> 连接状态: 已成功注册到中心节点 <<<")
		logger.Infof("    虚拟网络已建立，可以与同群组内其他节点通信")

		if cb != nil {
			cb(StateRegistered)
		}

		go e.postConnectCheck()
	}

	if strings.Contains(lineLower, "peer") && strings.Contains(lineLower, "added") {
		e.mu.Lock()
		e.registeredPeers++
		peers := e.registeredPeers
		e.mu.Unlock()
		logger.Infof(">>> 节点发现: 发现新节点 (当前群内共 %d 个节点) <<<", peers)
	}

	if strings.Contains(lineLower, "already in use") ||
		strings.Contains(lineLower, "not released yet") {
		logger.Warnf(">>> 连接警告: %s <<<", line)
		e.scheduleRegistrationRetry()
		return
	}

	if strings.Contains(lineLower, "error") ||
		strings.Contains(lineLower, "failed") ||
		strings.Contains(lineLower, "cannot") {
		logger.Warnf(">>> 连接警告: %s <<<", line)

		// 仅在尚未注册时才标记为错误状态
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
	// 使用 chcp 65001 切换到 UTF-8 编码，避免中文乱码
	cmd := exec.Command("cmd", "/C", "chcp 65001 >nul && ping -n 1 -w 2000 "+ip)
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
		logger.Infof("  配置 TAP 适配器成功: %s/24, MTU=1290", cfg.IP)
	}

	logger.Infof(">>> TAP 适配器配置完成 <<<")
}
