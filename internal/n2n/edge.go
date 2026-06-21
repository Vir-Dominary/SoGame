package n2n

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"sogame/internal/config"
	"sogame/internal/logger"
	"sogame/internal/platform"

	"golang.org/x/sys/windows"
)

type StatusCallback func(isRunning bool, message string)
type ConnectionStateCallback func(state ConnectionState)

type ConnectionState int

const (
	authRetryDelay       = 5 * time.Second
	maxAuthConflictRetry = 2
	newProcessGroupFlag  = 0x00000200

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
	cmd                      *exec.Cmd
	mu                       sync.Mutex
	stopMu                   sync.Mutex
	done                     chan struct{}
	callback                 StatusCallback
	connectionStateCallback  ConnectionStateCallback
	isHealthy                bool
	lastHealthCheck          time.Time
	config                   *config.Config
	autoRestart              bool
	restartCount             int
	maxRestarts              int
	restartCooldown          time.Duration
	manualStop               bool
	connectionState          ConnectionState
	registeredPeers          int
	tapConfigured            bool // 标记 TAP 是否已配置，防止重复配置
	authConflictRetries      int
	registrationRetryPending bool
	mgmtPort                 int
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
	if tapName := platform.FindTapInterfaceName(); tapName != "" {
		args = append(args, "-d", tapName)
	}

	// 管理端口（用于优雅退出发送 stop 命令）
	if cfg.MgmtPort > 0 {
		args = append(args, "-t", strconv.Itoa(cfg.MgmtPort))
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
	if err := KillOrphanEdgeProcess(); err != nil {
		logger.Warnf("failed to kill orphan edge process: %v", err)
	}
	e.mu.Lock()

	e.manualStop = false
	e.connectionState = StateConnecting
	e.registeredPeers = 0
	// 注册冲突重试计数器管理：
	// - 若本次 Start() 不是注册冲突重试（registrationRetryPending==false），
	//   说明是新连接或用户手动重连，应清零计数器。
	// - 若是注册冲突重试（registrationRetryPending==true），保留计数器，
	//   让 scheduleRegistrationRetry 能正确累加到 maxAuthConflictRetry 后停止。
	// - 无论哪种情况，Start() 已开始处理，registrationRetryPending 标志不再需要。
	if !e.registrationRetryPending {
		e.authConflictRetries = 0
	}
	e.registrationRetryPending = false
	e.config = cfg

	// 分配管理端口（用于优雅退出）
	mgmtPort, err := allocateUDPPort()
	if err != nil {
		logger.Warnf("failed to allocate management port: %v, graceful shutdown may not work", err)
		mgmtPort = 0
	}
	e.mgmtPort = mgmtPort
	cfg.MgmtPort = mgmtPort

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

	// CREATE_NEW_PROCESS_GROUP：使 edge 进程独立于父进程的信号组，
	// 允许后续通过 GenerateConsoleCtrlEvent 发送 CTRL_BREAK_EVENT。
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

	// 保存运行时状态到文件，用于清理孤儿进程（包含 PID、管理端口、启动时间戳）
	saveEdgeRuntime(cmd.Process.Pid, mgmtPort)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			logger.Infof("[EDGE] %s", line)
			e.parseEdgeOutput(line)
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
			e.parseEdgeOutput(line)
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

	// TAP 配置延迟到注册成功后触发（见 parseEdgeOutput 中的触发逻辑）
	// 同时启动一个兜底协程：如果 10 秒内未检测到注册成功，也尝试配置 TAP
	go e.deferredTapConfig(cfg)

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
	e.tapConfigured = false
	e.mgmtPort = 0
	// 注意：不清零 registrationRetryPending 和 authConflictRetries。
	// 这两个字段由 Start() 在判断后管理，避免 Reset() 提前清零导致
	// Start() 误把 authConflictRetries 重置为 0（注册冲突重试计数器归零 Bug）。
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
	mgmtPort := e.mgmtPort
	e.mu.Unlock()

	logger.Infof("stopping edge process (PID: %d, mgmtPort: %d)", pid, mgmtPort)

	if err := terminateEdgeProcessWindows(pid, done, mgmtPort); err != nil {
		logger.Warnf("terminateEdgeProcessWindows failed: %v", err)
		return err
	}

	logger.Infof("edge process terminated successfully (PID: %d)", pid)
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

// edgeRuntimeState 保存 edge 进程运行时状态，用于孤儿进程清理和 PID 复用防护。
type edgeRuntimeState struct {
	PID       int   `json:"pid"`
	MgmtPort  int   `json:"mgmt_port,omitempty"`
	StartedAt int64 `json:"started_at,omitempty"` // 进程创建时间（纳秒），用于 PID 复用防护
	Legacy    bool  `json:"-"`                    // 标记为旧格式（仅 PID，无时间戳）
}

// saveEdgeRuntime 保存 edge 进程运行时状态到文件
func saveEdgeRuntime(pid, mgmtPort int) {
	path, err := edgePIDPath()
	if err != nil {
		logger.Warnf("failed to get edge PID file path: %v", err)
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)

	state := edgeRuntimeState{PID: pid, MgmtPort: mgmtPort}
	if startedAt, err := processStartTime(pid); err == nil && startedAt > 0 {
		state.StartedAt = startedAt
	}
	data, err := json.Marshal(state)
	if err != nil {
		logger.Warnf("failed to marshal edge runtime state: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		logger.Warnf("failed to save edge runtime state: %v", err)
	}
}

// loadEdgeRuntime 读取 edge 进程运行时状态
func loadEdgeRuntime() *edgeRuntimeState {
	path, err := edgePIDPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// 尝试 JSON 格式（新格式）
	var state edgeRuntimeState
	if err := json.Unmarshal(data, &state); err == nil && state.PID > 0 {
		return &state
	}

	// 回退到旧格式（纯 PID 字符串）
	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return nil
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return nil
	}
	return &edgeRuntimeState{PID: pid, Legacy: true}
}

// clearEdgeRuntime 清除 edge 进程运行时状态文件
func clearEdgeRuntime() {
	path, err := edgePIDPath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

// 兼容旧代码
func saveEdgePID(pid int) { saveEdgeRuntime(pid, 0) }
func clearEdgePID()       { clearEdgeRuntime() }

// KillOrphanEdgeProcess 通过运行时状态文件清理上次运行遗留的 edge 进程。
// 使用 PID 复用防护（进程启动时间戳），避免误杀 PID 被复用的其他进程。
func KillOrphanEdgeProcess() error {
	state := loadEdgeRuntime()
	if state == nil || state.PID <= 0 {
		return nil // 无运行时状态文件，无需清理
	}

	pid := state.PID

	// 旧格式（仅 PID，无时间戳）：跳过终止，仅清除文件，避免误杀
	if state.Legacy || state.StartedAt == 0 {
		logger.Warnf("found legacy edge PID file (PID: %d), clearing without kill to avoid PID reuse risk", pid)
		clearEdgeRuntime()
		return nil
	}

	// 验证进程是否仍然是我们启动的 edge（PID 复用防护）
	if !runtimeStateMatches(state) {
		logger.Infof("edge PID %d no longer belongs to our edge process (PID reused), clearing state file", pid)
		clearEdgeRuntime()
		return nil
	}

	logger.Infof("found orphan edge process (PID: %d, mgmtPort: %d), terminating...", pid, state.MgmtPort)
	if err := terminateEdgeProcessWindows(pid, nil, state.MgmtPort); err != nil {
		logger.Warnf("failed to terminate orphan edge process (PID: %d): %v", pid, err)
		clearEdgeRuntime()
		return err
	}

	logger.Infof("orphan edge process terminated (PID: %d)", pid)
	clearEdgeRuntime()
	return nil
}

func (e *Edge) parseEdgeOutput(line string) {
	lineLower := strings.ToLower(line)

	// 优先检测注册成功状态（这是最终成功状态）
	// n2n edge v3 多种输出格式：
	//   [OK] edge <<< ================ >>> supernode
	//   registered with supernode
	//   successfully registered
	//   连接状态: 已成功注册到中心节点
	if strings.Contains(lineLower, "registered with supernode") ||
		strings.Contains(lineLower, "successfully registered") ||
		(strings.Contains(lineLower, "<<<") && strings.Contains(lineLower, ">>>") && strings.Contains(lineLower, "supernode")) ||
		strings.Contains(lineLower, "连接状态: 已成功注册到中心节点") ||
		strings.Contains(lineLower, "edge_operate") && strings.Contains(lineLower, "supernode") && !strings.Contains(lineLower, "error") ||
		strings.Contains(lineLower, "supernode0:") && strings.Contains(lineLower, "ok") ||
		(strings.Contains(lineLower, "ok") && strings.Contains(lineLower, "edge") && strings.Contains(lineLower, "supernode")) {
		e.mu.Lock()
		if e.connectionState == StateRegistered {
			e.mu.Unlock()
			return
		}
		prevState := e.connectionState
		e.connectionState = StateRegistered
		cb := e.connectionStateCallback
		e.mu.Unlock()

		if prevState != StateRegistered {
			logger.Infof("状态转换: %s -> Connected (已成功注册到中心节点)", prevState.String())
		}
		logger.Infof(">>> 连接状态: 已成功注册到中心节点 <<<")
		logger.Infof("    虚拟网络已建立，可以与同群组内其他节点通信")

		if cb != nil {
			cb(StateRegistered)
		}

		go e.postConnectCheck()

		// 注册成功后触发 TAP 配置（包括 IP、MTU、防火墙规则、网络类别）
		e.mu.Lock()
		cfg := e.config
		e.mu.Unlock()
		if cfg != nil {
			go e.configureTapInterface(cfg)
		}

		return
	}

	// 检测 TCP 连接成功（中间状态，不是最终成功）
	if strings.Contains(lineLower, "connected to supernode") ||
		strings.Contains(lineLower, "supernode connection established") {
		e.mu.Lock()
		// 只有在 Connecting 状态下才升级为 Connected
		// 如果已经 Registered，不降级
		if e.connectionState != StateRegistered {
			prevState := e.connectionState
			e.connectionState = StateConnected
			cb := e.connectionStateCallback
			e.mu.Unlock()
			logger.Infof("状态转换: %s -> Connected (已连接到中心节点)", prevState.String())
			if cb != nil {
				cb(StateConnected)
			}
		} else {
			e.mu.Unlock()
		}
		return
	}

	// 检测正在连接
	if strings.Contains(lineLower, "connecting to supernode") ||
		strings.Contains(lineLower, "resolving supernode") {
		e.mu.Lock()
		prevState := e.connectionState
		e.connectionState = StateConnecting
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Infof("状态转换: %s -> Connecting", prevState.String())

		if cb != nil {
			cb(StateConnecting)
		}
		return
	}

	// 检测节点发现
	if strings.Contains(lineLower, "peer") && strings.Contains(lineLower, "added") {
		e.mu.Lock()
		e.registeredPeers++
		peers := e.registeredPeers
		e.mu.Unlock()
		logger.Infof(">>> 节点发现: 发现新节点 (当前群内共 %d 个节点) <<<", peers)
		return
	}

	// 检测错误——仅在未注册成功时才标记为错误
	// 注册成功后，edge 可能输出包含 "error" 的非关键日志（如心跳超时重试）
	// 注意：使用更精确的模式匹配，避免误判 "no error"、"error_count" 等非错误行
	isError := strings.Contains(lineLower, "error:") ||
		strings.Contains(lineLower, "error -") ||
		strings.Contains(lineLower, "error: ") ||
		strings.Contains(lineLower, "fatal error") ||
		strings.Contains(lineLower, "connection failed") ||
		strings.Contains(lineLower, "failed to") ||
		strings.Contains(lineLower, "cannot connect") ||
		strings.Contains(lineLower, "cannot resolve") ||
		strings.Contains(lineLower, "cannot register")
	if isError {
		// 检测认证冲突（IP/MAC 已被占用），触发自动重试
		isAuthConflict := strings.Contains(lineLower, "authentication error") ||
			(strings.Contains(lineLower, "already in use") && strings.Contains(lineLower, "supernode")) ||
			strings.Contains(lineLower, "not released yet")

		e.mu.Lock()
		if e.connectionState != StateRegistered && e.connectionState != StateConnected {
			prevState := e.connectionState
			e.connectionState = StateError
			cb := e.connectionStateCallback
			shouldRetry := isAuthConflict && !e.registrationRetryPending
			e.mu.Unlock()
			logger.Warnf("状态转换: %s -> Error (%s)", prevState.String(), line)
			if cb != nil {
				cb(StateError)
			}
			// 认证冲突时自动重试（IP/MAC 可能因上次未优雅退出被占用，等待释放后重试）
			if shouldRetry {
				go e.scheduleRegistrationRetry()
			}
		} else {
			e.mu.Unlock()
			logger.Debugf("[EDGE 非关键警告] %s", line)
		}
		return
	}

	// 检测断开连接
	if strings.Contains(lineLower, "disconnected") || strings.Contains(lineLower, "connection lost") {
		e.mu.Lock()
		prevState := e.connectionState
		e.connectionState = StateDisconnected
		cb := e.connectionStateCallback
		e.mu.Unlock()
		logger.Warnf("状态转换: %s -> Disconnected", prevState.String())

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
		if e.connectionState == StateRegistered {
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
	// 防止重复配置
	e.mu.Lock()
	if e.tapConfigured {
		e.mu.Unlock()
		return
	}
	e.tapConfigured = true
	e.mu.Unlock()

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

		// TAP 配置成功意味着 edge 已经建立了虚拟网络
		// 如果 parseEdgeOutput 尚未检测到注册成功，在此推断为已连接
		e.mu.Lock()
		if e.connectionState == StateConnecting || e.connectionState == StateConnected {
			prevState := e.connectionState
			e.connectionState = StateRegistered
			cb := e.connectionStateCallback
			e.mu.Unlock()
			logger.Infof("状态转换: %s -> Connected (TAP 配置成功，推断已注册)", prevState.String())
			if cb != nil {
				cb(StateRegistered)
			}
		} else {
			e.mu.Unlock()
		}
	}

	// 添加防火墙入站规则，允许 TAP 适配器上的所有流量
	if err := platform.AddFirewallRule(ifName); err != nil {
		logger.Warnf("  添加防火墙规则失败: %v (可能影响双向连通性)", err)
	}

	// 将 TAP 适配器网络类别设置为"专用"，放宽入站限制
	if err := platform.SetNetworkCategoryPrivate(ifName); err != nil {
		logger.Warnf("  设置网络类别为专用失败: %v (可能影响双向连通性)", err)
	}

	logger.Infof(">>> TAP 适配器配置完成 <<<")
}

// deferredTapConfig 是兜底协程：如果 10 秒内 parseEdgeOutput 未检测到注册成功，
// 仍然尝试配置 TAP。这覆盖了 edge 输出格式变化导致注册检测失败的场景。
func (e *Edge) deferredTapConfig(cfg *config.Config) {
	select {
	case <-time.After(10 * time.Second):
		e.mu.Lock()
		configured := e.tapConfigured
		e.mu.Unlock()
		if !configured {
			logger.Warnf("10 秒内未检测到注册成功，尝试兜底配置 TAP")
			go e.configureTapInterface(cfg)
		}
	case <-e.done:
		// edge 进程已退出，无需配置
	}
}

// NodeLatencyInfo 节点延迟信息
type NodeLatencyInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Latency int    `json:"latency"` // 毫秒，-1 表示不可用
}

// MeasureNodeLatency 测量到指定 supernode 的网络延迟（ICMP ping）
// 连续测试 3 次，取平均值
func MeasureNodeLatency(address string) int {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}

	// 解析主机名，处理 IPv6 地址
	addrs, err := net.LookupHost(host)
	if err != nil {
		return -1
	}

	// 优先使用 IPv4 地址
	pingTarget := host
	for _, addr := range addrs {
		if !strings.Contains(addr, ":") {
			pingTarget = addr
			break
		}
	}

	const attempts = 3
	var totalMs int
	successCount := 0

	for i := 0; i < attempts; i++ {
		ms := pingHost(pingTarget)
		if ms >= 0 {
			totalMs += ms
			successCount++
		}
	}

	if successCount == 0 {
		return -1
	}

	return totalMs / successCount
}

// pingHost 对目标主机执行一次 ICMP ping，返回延迟毫秒数
// 使用 Windows 系统 ping 命令，解析输出中的延迟值
func pingHost(host string) int {
	cmd := exec.Command("ping", "-n", "1", "-w", "2000", host)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return -1
	}

	return parsePingLatency(output)
}

// parsePingLatency 从 ping 输出中解析延迟毫秒数
// Windows 中文/英文 ping 输出使用 GBK 编码，不能直接按 UTF-8 解析中文
// 改用字节级模式匹配：查找 "time=" 或 "时间=" 后面的数字
// GBK 编码中 "时间=" 的字节序列为: 0xCA 0xB1 0xBC 0xE4 0x3D
func parsePingLatency(data []byte) int {
	// 搜索 "time=" (ASCII，英文 Windows)
	timePattern := []byte("time=")
	// 搜索 "时间=" 的 GBK 编码 (中文 Windows)
	timePatternGBK := []byte{0xCA, 0xB1, 0xBC, 0xE4, 0x3D}

	patterns := [][]byte{timePattern, timePatternGBK}

	for _, pattern := range patterns {
		idx := bytes.Index(data, pattern)
		if idx == -1 {
			continue
		}

		// 跳过 "time=" 或 "时间="，提取后面的数字
		after := data[idx+len(pattern):]

		// 跳过可能的空格
		i := 0
		for i < len(after) && after[i] == ' ' {
			i++
		}

		// 提取数字（支持小数，如 29.5ms）
		numStart := i
		for i < len(after) && ((after[i] >= '0' && after[i] <= '9') || after[i] == '.') {
			i++
		}

		if i > numStart {
			numStr := string(after[numStart:i])
			val, err := strconv.ParseFloat(numStr, 64)
			if err == nil {
				return int(val)
			}
		}
	}

	return -1
}

// MeasureAllNodesLatency 测量所有已知节点的延迟
func MeasureAllNodesLatency() []NodeLatencyInfo {
	nodes := GetKnownNodes()
	results := make([]NodeLatencyInfo, 0, len(nodes))

	logger.Infof("Node latency test:")

	for _, node := range nodes {
		latency := MeasureNodeLatency(node.Address)
		info := NodeLatencyInfo{
			Name:    node.Name,
			Address: node.Address,
			Latency: latency,
		}
		if latency >= 0 {
			logger.Infof("  %s: %dms", node.Name, latency)
		} else {
			logger.Infof("  %s: 不可用", node.Name)
		}
		results = append(results, info)
	}

	// 按延迟排序：可用节点按延迟升序，不可用节点排最后
	sort.Slice(results, func(i, j int) bool {
		iAvail := results[i].Latency >= 0
		jAvail := results[j].Latency >= 0
		if iAvail != jAvail {
			return iAvail
		}
		return results[i].Latency < results[j].Latency
	})

	return results
}

// allocateUDPPort 分配一个空闲的 UDP 端口（绑定到 0 端口让系统分配）
func allocateUDPPort() (int, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port, nil
}

// processStartTime 获取进程创建时间（纳秒），用于 PID 复用防护
func processStartTime(pid int) (int64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var creationTime, exitTime, kernelTime, userTime windows.Filetime
	if err := windows.GetProcessTimes(h, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return 0, err
	}
	return creationTime.Nanoseconds(), nil
}

// edgeProcessRunning 检查指定 PID 的进程是否仍在运行
func edgeProcessRunning(pid int) bool {
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH", "/FO", "CSV")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	// CSV 格式输出中 PID 带引号，如 "29316"
	return strings.Contains(string(output), fmt.Sprintf("\"%d\"", pid))
}

// runtimeStateMatches 验证 PID 是否仍属于我们启动的 edge 进程（PID 复用防护）
func runtimeStateMatches(state *edgeRuntimeState) bool {
	if !edgeProcessRunning(state.PID) {
		return false
	}
	if state.StartedAt == 0 {
		// 无时间戳，仅信任 PID 存在
		return true
	}
	startedAt, err := processStartTime(state.PID)
	if err != nil {
		return false
	}
	return startedAt == state.StartedAt
}

// sendMgmtStop 通过 UDP 向 edge 管理端口发送 stop 命令
func sendMgmtStop(port int) bool {
	if port <= 0 {
		return false
	}
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		logger.Warnf("failed to dial management port %d: %v", port, err)
		return false
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("stop\r\n")); err != nil {
		logger.Warnf("failed to send stop command: %v", err)
		return false
	}
	return true
}

// waitForProcessExit 等待进程退出，支持 done channel 和超时
func waitForProcessExit(pid int, done <-chan struct{}, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return nil
		case <-deadline:
			return fmt.Errorf("timeout waiting for process %d to exit", pid)
		case <-ticker.C:
			if !edgeProcessRunning(pid) {
				return nil
			}
		}
	}
}

// terminateEdgeProcessWindows 实现三层优雅退出策略：
//  1. 管理端口 stop 命令（3s）—— 让 edge 正常注销并释放 IP/MAC
//  2. CTRL_BREAK_EVENT 信号（2s）—— 控制台信号，GUI 应用可能无效
//  3. taskkill /F 强制终止（3s）—— 最后手段
func terminateEdgeProcessWindows(pid int, done <-chan struct{}, mgmtPort int) error {
	// 第一层：管理端口 stop
	if mgmtPort > 0 {
		logger.Infof("graceful shutdown tier 1: sending stop via management port %d", mgmtPort)
		if sendMgmtStop(mgmtPort) {
			if err := waitForProcessExit(pid, done, 3*time.Second); err == nil {
				logger.Infof("graceful shutdown tier 1 succeeded (mgmt port stop)")
				return nil
			}
			logger.Warnf("graceful shutdown tier 1 timed out")
		} else {
			logger.Warnf("graceful shutdown tier 1 failed to send stop command")
		}
	}

	// 检查进程是否已退出
	select {
	case <-done:
		return nil
	default:
	}
	if !edgeProcessRunning(pid) {
		return nil
	}

	// 第二层：CTRL_BREAK_EVENT
	logger.Infof("graceful shutdown tier 2: sending CTRL_BREAK_EVENT")
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid)); err != nil {
		logger.Warnf("graceful shutdown tier 2 failed: %v", err)
	} else {
		if err := waitForProcessExit(pid, done, 2*time.Second); err == nil {
			logger.Infof("graceful shutdown tier 2 succeeded (CTRL_BREAK)")
			return nil
		}
		logger.Warnf("graceful shutdown tier 2 timed out")
	}

	// 检查进程是否已退出
	select {
	case <-done:
		return nil
	default:
	}
	if !edgeProcessRunning(pid) {
		return nil
	}

	// 第三层：taskkill /F 强制终止
	logger.Infof("graceful shutdown tier 3: taskkill /F (force kill)")
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("force kill failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	if err := waitForProcessExit(pid, done, 3*time.Second); err != nil {
		return fmt.Errorf("process did not exit after force kill: %w", err)
	}
	logger.Infof("graceful shutdown tier 3 succeeded (force kill)")
	return nil
}

// scheduleRegistrationRetry 在检测到认证冲突（IP/MAC 已被占用）时自动重试
func (e *Edge) scheduleRegistrationRetry() {
	e.mu.Lock()
	if e.authConflictRetries >= maxAuthConflictRetry {
		e.mu.Unlock()
		logger.Warnf("auth conflict retry limit reached (%d/%d), giving up", e.authConflictRetries, maxAuthConflictRetry)
		return
	}
	e.authConflictRetries++
	e.registrationRetryPending = true
	cfg := e.config
	e.mu.Unlock()

	logger.Infof("scheduling auth conflict retry %d/%d after %v", e.authConflictRetries, maxAuthConflictRetry, authRetryDelay)

	time.AfterFunc(authRetryDelay, func() {
		if err := e.Start(cfg); err != nil {
			logger.Errorf("auth conflict retry failed: %v", err)
		}
	})
}
