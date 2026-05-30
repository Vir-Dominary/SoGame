package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"netjoin/internal/logger"

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

	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf("[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; try { $a = Get-NetAdapter -Name '%s' -ErrorAction Stop; if ($a.Status -ne $null) { Write-Output 'EXISTS' } else { Write-Output 'EXISTS' } } catch { Write-Output 'NOT_FOUND' }", SoGameAdapterName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	return strings.TrimSpace(string(output)) == "EXISTS"
}

// isTapDriverInstalled 检查 TAP 驱动是否已安装到系统中（不一定有 SoGame 适配器实例）
func isTapDriverInstalled() bool {
	cmd := exec.Command("powershell", "-Command",
		`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; (Get-NetAdapter -IncludeHidden | Where-Object { $_.InterfaceDescription -match 'TAP-Windows|tap0901' } | Measure-Object).Count`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != "0"
}

// isTapAdapterInstalled 检查是否存在任何 TAP 适配器
func isTapAdapterInstalled() bool {
	if !IsWindows() {
		return true
	}

	if IsSoGameAdapterExists() {
		return true
	}

	cmd := exec.Command("powershell", "-Command",
		`Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'tap|wintun|tun' } | Select-Object -First 1 -ExpandProperty Name`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err == nil && len(strings.TrimSpace(string(output))) > 0 {
		return true
	}

	return false
}

// FindTapInterfaceName 查找 TAP 接口名，优先返回 SoGame 专属适配器
func FindTapInterfaceName() string {
	if !IsWindows() {
		return ""
	}

	// 优先查找 SoGame 专属适配器
	if IsSoGameAdapterExists() {
		logger.Debugf("found SoGame dedicated adapter: %s", SoGameAdapterName)
		return SoGameAdapterName
	}

	// 回退：查找任何可用的 TAP 适配器（优先选择没有 IP 的）
	cmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $adapters = Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'tap|wintun|tun' -and $_.Name -ne '%s' }; foreach ($a in $adapters) { $ip = (Get-NetIPAddress -InterfaceAlias $a.Name -AddressFamily IPv4 -ErrorAction SilentlyContinue).IPAddress; if (-not $ip) { Write-Output $a.Name; break } }; if (-not $?) { foreach ($a in $adapters) { Write-Output $a.Name; break } }`, SoGameAdapterName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err == nil {
		name := strings.TrimSpace(string(output))
		if name != "" {
			logger.Debugf("found TAP interface (fallback): %s", name)
			return name
		}
	}

	return ""
}

// EnableTapInterface 启用可能被禁用的 TAP 网络适配器
func EnableTapInterface(ifName string) {
	if !IsWindows() || ifName == "" {
		return
	}

	checkCmd := exec.Command("powershell", "-Command",
		fmt.Sprintf("[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue).Status", ifName))
	checkCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := checkCmd.CombinedOutput()
	if err == nil {
		status := strings.TrimSpace(string(output))
		if strings.EqualFold(status, "Up") {
			return
		}
		logger.Infof("TAP 适配器 '%s' 当前状态: %s，正在启用...", ifName, status)
	}

	enableCmd := exec.Command("netsh", "interface", "set", "interface", ifName, "enable")
	enableCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	enableOutput, enableErr := enableCmd.CombinedOutput()
	if enableErr != nil {
		logger.Warnf("netsh enable interface failed: %v, %s", enableErr, strings.TrimSpace(string(enableOutput)))

		psCmd := exec.Command("powershell", "-Command",
			fmt.Sprintf("Enable-NetAdapter -Name '%s' -Confirm:$false", ifName))
		psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		psOutput, psErr := psCmd.CombinedOutput()
		if psErr != nil {
			logger.Warnf("PowerShell Enable-NetAdapter failed: %v, %s", psErr, strings.TrimSpace(string(psOutput)))
		} else {
			logger.Infof("TAP 适配器 '%s' 已通过 PowerShell 启用", ifName)
		}
	} else {
		logger.Infof("TAP 适配器 '%s' 已通过 netsh 启用", ifName)
	}

	time.Sleep(1 * time.Second)
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

	EnableTapInterface(ifName)

	resetCmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "dhcp")
	resetCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	resetCmd.CombinedOutput()

	time.Sleep(500 * time.Millisecond)

	cmd := exec.Command("netsh", "interface", "ip", "set", "address",
		ifName, "static", ip, "255.255.255.0")
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
		EnableTapInterface(SoGameAdapterName)
		return TapAlreadyInstalled, nil
	}

	logger.Infof("正在创建 SoGame 专属 TAP 适配器 '%s'...", SoGameAdapterName)

	// 尝试1：将现有的未命名 TAP 适配器重命名为 SoGame-VPN
	renameCmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $tap = Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'TAP-Windows|tap0901|tap-windows' -and $_.Name -ne '%s' } | Select-Object -First 1; if ($tap) { Rename-NetAdapter -Name $tap.Name -NewName '%s' -PassThru | Select-Object -ExpandProperty Name } else { Write-Output 'NOT_FOUND' }`, SoGameAdapterName, SoGameAdapterName))
	renameCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	renameOutput, renameErr := renameCmd.CombinedOutput()

	if renameErr == nil {
		renamedName := strings.TrimSpace(string(renameOutput))
		if renamedName == SoGameAdapterName {
			logger.Infof("已将现有 TAP 适配器重命名为 '%s'", SoGameAdapterName)
			EnableTapInterface(SoGameAdapterName)
			return TapInstallSuccess, nil
		}
	} else {
		logger.Warnf("重命名现有适配器失败: %v, %s", renameErr, strings.TrimSpace(string(renameOutput)))
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
	renameCmd := exec.Command("powershell", "-Command",
		fmt.Sprintf(`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $tap = Get-NetAdapter | Where-Object { $_.InterfaceDescription -match 'TAP-Windows|tap0901' -and $_.Name -ne '%s' } | Select-Object -First 1; if ($tap) { Rename-NetAdapter -Name $tap.Name -NewName '%s' -PassThru | Select-Object -ExpandProperty Name } else { Write-Output 'NOT_FOUND' }`, SoGameAdapterName, SoGameAdapterName))
	renameCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	renameOutput, renameErr := renameCmd.CombinedOutput()

	if renameErr != nil {
		logger.Warnf("  重命名适配器失败: %v, %s", renameErr, strings.TrimSpace(string(renameOutput)))
	} else {
		renamedName := strings.TrimSpace(string(renameOutput))
		if renamedName == SoGameAdapterName {
			logger.Infof("  TAP 适配器已重命名为 '%s'", SoGameAdapterName)
		} else if renamedName == "NOT_FOUND" {
			logger.Warnf("  未找到可重命名的 TAP 适配器")
		} else {
			logger.Warnf("  重命名结果异常: %s", renamedName)
		}
	}

	// 启用适配器并设置跃点数
	EnableTapInterface(SoGameAdapterName)

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
