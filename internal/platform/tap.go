package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"sogame/internal/logger"
	"sogame/internal/nic"
	tapadapter "sogame/internal/tap"

	"golang.org/x/sys/windows"
)

const SoGameAdapterName = "SoGame-VPN"

func IsWindows() bool {
	return runtime.GOOS == "windows"
}

func CheckAdminPrivileges() bool {
	if !IsWindows() {
		return true
	}

	var token windows.Token
	currentProcess, _ := windows.GetCurrentProcess()
	err := windows.OpenProcessToken(currentProcess, windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()

	var elevation uint32
	var returnedLen uint32
	err = windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &returnedLen)
	if err != nil {
		return false
	}

	return elevation != 0
}

// IsSoGameAdapterExists 检查 SoGame 专属 TAP 适配器是否存在
func IsSoGameAdapterExists() bool {
	if !IsWindows() {
		return true
	}

	_, err := nic.FindByFriendlyName(SoGameAdapterName)
	if err == nil {
		return true
	}
	if !errors.Is(err, nic.ErrNotFound) {
		logger.Warnf("查找 SoGame 专属适配器 '%s' 失败: %v", SoGameAdapterName, err)
	}
	return false
}

func renameTapAdapterByNic() bool {
	info, err := tapadapter.RenameCandidate(SoGameAdapterName, 3*time.Second)
	if err != nil {
		if !errors.Is(err, nic.ErrNotFound) {
			logger.Warnf("重命名 TAP 适配器为 '%s' 失败: %v", SoGameAdapterName, err)
		}
		return false
	}

	logger.Infof("已将 TAP 适配器 '%s' 重命名为 '%s'", info.FriendlyName, SoGameAdapterName)
	return true
}

// isTapDriverInstalled 检查系统中是否已有 TAP-Windows 适配器实例。
func isTapDriverInstalled() bool {
	if !IsWindows() {
		return true
	}

	ok, err := tapadapter.HasWindowsAdapter()
	if err != nil {
		logger.Warnf("查询 TAP 驱动实例失败: %v", err)
		return false
	}
	return ok
}

// isTapAdapterInstalled 检查是否存在任何 TAP 适配器
func isTapAdapterInstalled() bool {
	if !IsWindows() {
		return true
	}

	_, err := tapadapter.FindAdapter(SoGameAdapterName)
	if err == nil {
		return true
	}
	if !errors.Is(err, nic.ErrNotFound) {
		logger.Warnf("查找 TAP 适配器失败: %v", err)
	}
	return false
}

// FindTapInterfaceName 查找 TAP 接口名，优先返回 SoGame 专属适配器
func FindTapInterfaceName() string {
	if !IsWindows() {
		return ""
	}

	info, err := tapadapter.FindAdapter(SoGameAdapterName)
	if err != nil {
		if !errors.Is(err, nic.ErrNotFound) {
			logger.Warnf("查找 TAP 接口失败: %v", err)
		}
		return ""
	}

	if strings.EqualFold(info.FriendlyName, SoGameAdapterName) {
		logger.Debugf("found SoGame dedicated adapter by nic: %s", info.FriendlyName)
	} else {
		logger.Debugf("found TAP interface by nic: %s", info.FriendlyName)
	}
	return info.FriendlyName
}

// RestartTapInterface 重启 TAP 网络适配器以清空设备层残留状态。
func RestartTapInterface(ifName string) error {
	if !IsWindows() || ifName == "" {
		return nil
	}

	const attempts = 2
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		logger.Infof("正在通过设备层 API 重启 TAP 适配器 '%s' (%d/%d)...", ifName, attempt, attempts)
		if err := tapadapter.RestartAdapter(context.Background(), ifName, 10*time.Second); err != nil {
			lastErr = err
			if attempt < attempts {
				logger.Warnf("设备层重启 TAP 适配器 '%s' 失败，将重试: %v", ifName, err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			logger.Warnf("设备层重启 TAP 适配器 '%s' 失败: %v", ifName, err)
			return err
		}
		logger.Infof("TAP 适配器 '%s' 已通过设备层 API 重启", ifName)
		return nil
	}
	return lastErr
}

// EnableTapInterface 兼容旧接口；当前实现会执行设备层重启。
func EnableTapInterface(ifName string) {
	_ = RestartTapInterface(ifName)
}

// SetInterfaceMetric 设置网卡的跃点数（优先级），值越小优先级越高
func SetInterfaceMetric(ifName string, metric int) error {
	if !IsWindows() {
		return nil
	}

	cmd := exec.Command("netsh", "interface", "ipv4", "set", "interface",
		ifName, fmt.Sprintf("metric=%d", metric))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("设置跃点数失败: %v, %s", err, strings.TrimSpace(string(output)))
	}

	logger.Infof("TAP 适配器 '%s' 跃点数已设置为 %d", ifName, metric)
	return nil
}

// ConfigureTapInterface 配置 TAP 适配器的 IP 地址和 MTU
func ConfigureTapInterface(ifName, ip string) error {
	if !IsWindows() {
		return nil
	}

	resetCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "dhcp")
	resetCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	resetCmd.CombinedOutput()

	time.Sleep(500 * time.Millisecond)

	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "static", ip, "255.255.0.0")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}

	cmd = exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		ifName, "mtu=1290", "store=persistent")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}

	if err := SetInterfaceMetric(ifName, 1); err != nil {
		logger.Warnf("设置跃点数失败: %v", err)
	}

	return nil
}

type TapInstallStatus int

const (
	TapInstallSuccess TapInstallStatus = iota
	TapAlreadyInstalled
	TapInstallFailed
)

func IsNetworkAdapterReady() bool {
	if !IsWindows() {
		return true
	}
	return isTapAdapterInstalled()
}

// EnsureSoGameAdapter 确保存在 SoGame 专属 TAP 适配器
// 如果不存在，则将现有 TAP 适配器重命名或创建新的
func EnsureSoGameAdapter() (TapInstallStatus, error) {
	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 已存在", SoGameAdapterName)
		if err := RestartTapInterface(SoGameAdapterName); err != nil {
			return TapInstallFailed, fmt.Errorf("重启 SoGame 专属适配器失败: %w", err)
		}
		return TapAlreadyInstalled, nil
	}

	logger.Infof("正在创建 SoGame 专属 TAP 适配器 '%s'...", SoGameAdapterName)

	// 尝试1：将现有的未命名 TAP 适配器重命名为 SoGame-VPN
	if renameTapAdapterByNic() {
		if err := RestartTapInterface(SoGameAdapterName); err != nil {
			return TapInstallFailed, fmt.Errorf("重启 SoGame 专属适配器失败: %w", err)
		}
		return TapInstallSuccess, nil
	}

	// 尝试2：如果没有 TAP 驱动，先安装驱动
	if !isTapDriverInstalled() {
		status, err := installTapDriver()
		if err != nil {
			return status, err
		}
	}

	// 尝试3：创建新的 TAP 适配器实例并重命名
	return createSoGameAdapter()
}

// installTapDriver 安装 TAP 驱动到系统驱动存储
func installTapDriver() (TapInstallStatus, error) {
	exePath, err := os.Executable()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("failed to get executable path: %w", err)
	}
	baseDir := filepath.Dir(exePath)
	wd, _ := os.Getwd()

	tapDirCandidates := []string{
		filepath.Join(baseDir, "tap"),
		filepath.Join(baseDir, "installer", "tap"),
		filepath.Join(baseDir, "..", "installer", "tap"),
	}
	if wd != "" && wd != baseDir {
		tapDirCandidates = append(tapDirCandidates,
			filepath.Join(wd, "tap"),
			filepath.Join(wd, "installer", "tap"),
		)
	}

	var tapDir string
	for _, p := range tapDirCandidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(filepath.Join(abs, "OemWin2k.inf")); err == nil {
			tapDir = abs
			break
		}
	}

	if tapDir == "" {
		return TapInstallFailed, fmt.Errorf("未找到 TAP 驱动文件目录 (OemWin2k.inf)")
	}

	logger.Infof("正在安装 TAP 驱动，驱动目录: %s", tapDir)

	infPath := filepath.Join(tapDir, "OemWin2k.inf")

	logger.Infof("  添加 TAP 驱动到驱动存储...")
	pnputilCmd := exec.Command("pnputil", "/add-driver", infPath, "/install")
	pnputilCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	pnputilOutput, pnputilErr := pnputilCmd.CombinedOutput()
	if pnputilErr != nil {
		logger.Warnf("  pnputil /add-driver 失败: %v, 输出: %s", pnputilErr, strings.TrimSpace(string(pnputilOutput)))
		logger.Infof("  重试: 使用 /force 标志...")
		pnputilCmd2 := exec.Command("pnputil", "/add-driver", infPath, "/install", "/force")
		pnputilCmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		pnputilOutput2, pnputilErr2 := pnputilCmd2.CombinedOutput()
		if pnputilErr2 != nil {
			logger.Warnf("  pnputil /add-driver /force 也失败: %v, 输出: %s", pnputilErr2, strings.TrimSpace(string(pnputilOutput2)))
		} else {
			logger.Infof("  pnputil /add-driver /force 成功")
		}
	} else {
		logger.Infof("  pnputil /add-driver 成功")
	}

	return TapInstallSuccess, nil
}

// createSoGameAdapter 创建 TAP 适配器实例并重命名为 SoGame-VPN
func createSoGameAdapter() (TapInstallStatus, error) {
	exePath, err := os.Executable()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("failed to get executable path: %w", err)
	}
	baseDir := filepath.Dir(exePath)
	wd, _ := os.Getwd()

	tapDirCandidates := []string{
		filepath.Join(baseDir, "tap"),
		filepath.Join(baseDir, "installer", "tap"),
		filepath.Join(baseDir, "..", "installer", "tap"),
	}
	if wd != "" && wd != baseDir {
		tapDirCandidates = append(tapDirCandidates,
			filepath.Join(wd, "tap"),
			filepath.Join(wd, "installer", "tap"),
		)
	}

	var tapDir string
	for _, p := range tapDirCandidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(filepath.Join(abs, "OemWin2k.inf")); err == nil {
			tapDir = abs
			break
		}
	}

	if tapDir == "" {
		return TapInstallFailed, fmt.Errorf("未找到 TAP 驱动文件目录 (OemWin2k.inf)")
	}

	infPath := filepath.Join(tapDir, "OemWin2k.inf")

	tapinstallCandidates := []string{
		filepath.Join(tapDir, "tapinstall.exe"),
		filepath.Join(tapDir, "devcon.exe"),
		`C:\Program Files\TAP-Windows\bin\tapinstall.exe`,
		`C:\Program Files\OpenVPN\bin\tapinstall.exe`,
	}

	var tapinstallPath string
	for _, p := range tapinstallCandidates {
		if _, err := os.Stat(p); err == nil {
			tapinstallPath = p
			break
		}
	}

	if tapinstallPath == "" {
		return TapInstallFailed, fmt.Errorf("未找到 tapinstall.exe")
	}

	logger.Infof("  创建 TAP 适配器实例: %s", tapinstallPath)

	installCmd := exec.Command(tapinstallPath, "install", infPath, "tap0901")
	installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	installOutput, installErr := installCmd.CombinedOutput()

	if installErr != nil {
		outputStr := strings.TrimSpace(string(installOutput))
		logger.Warnf("  tapinstall install 失败: %v, 输出: %s", installErr, outputStr)

		logger.Infof("  重试 tapinstall install...")
		time.Sleep(2 * time.Second)

		installCmd2 := exec.Command(tapinstallPath, "install", infPath, "tap0901")
		installCmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		installOutput2, installErr2 := installCmd2.CombinedOutput()

		if installErr2 != nil {
			outputStr2 := strings.TrimSpace(string(installOutput2))
			return TapInstallFailed, fmt.Errorf("TAP 适配器安装失败: %v\n输出: %s", installErr2, outputStr2)
		}
	}

	time.Sleep(3 * time.Second)

	// 查找新创建的 TAP 适配器并重命名为 SoGame-VPN
	if !renameTapAdapterByNic() {
		logger.Warnf("  未能将 TAP 适配器重命名为 '%s'", SoGameAdapterName)
	}

	// 重启适配器以清空残留状态
	if err := RestartTapInterface(SoGameAdapterName); err != nil {
		return TapInstallFailed, fmt.Errorf("重启 SoGame 专属适配器失败: %w", err)
	}

	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 创建成功", SoGameAdapterName)
		return TapInstallSuccess, nil
	}

	logger.Warnf("SoGame 专属适配器创建完成但验证未通过，将尝试继续连接")
	return TapInstallSuccess, nil
}

// InstallTapAdapter 兼容旧接口：确保 SoGame 专属适配器存在
func InstallTapAdapter() (TapInstallStatus, error) {
	return EnsureSoGameAdapter()
}
