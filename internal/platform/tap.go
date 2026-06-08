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

	"sogame/internal/ipconfig"
	"sogame/internal/logger"
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

	return tapadapter.ExistsByName(SoGameAdapterName)
}

// isTapDriverInstalled 检查 TAP 驱动是否已安装到系统中（不一定有 SoGame 适配器实例）
func isTapDriverInstalled() bool {
	return tapadapter.HasWindowsAdapter()
}

// isTapAdapterInstalled 检查是否存在任何 TAP 适配器
func isTapAdapterInstalled() bool {
	if !IsWindows() {
		return true
	}

	return tapadapter.HasAnyAdapter(SoGameAdapterName)
}

// FindTapInterfaceName 通过已知 GUID 查找 SoGame TAP 当前接口名
func FindTapInterfaceName() string {
	if !IsWindows() {
		return ""
	}

	resolved, err := tapadapter.ResolveKnownAdapter(SoGameAdapterName)
	if err != nil {
		logger.Warnf("按 GUID 查找 TAP 接口失败: %v", err)
		return ""
	}
	if resolved.Status == tapadapter.ResolveFound && resolved.Info != nil {
		logger.Debugf("found SoGame TAP by GUID: %s", resolved.Info.FriendlyName)
		return resolved.Info.FriendlyName
	}

	return ""
}

// EnableTapInterface 启用可能被禁用的 TAP 网络适配器
func EnableTapInterface(ifName string) {
	if !IsWindows() || ifName == "" {
		return
	}

	if err := tapadapter.EnableAdapterByName(ifName); err != nil {
		logger.Warnf("启用 TAP 适配器 '%s' 失败: %v", ifName, err)
		return
	}
	logger.Infof("TAP 适配器 '%s' 已启用", ifName)
}

// SetInterfaceMetric 设置网卡的跃点数（优先级），值越小优先级越高
func SetInterfaceMetric(ifName string, metric int) error {
	return ipconfig.SetInterfaceMetric(ifName, metric)
}

// ConfigureTapInterface 配置 TAP 适配器的 IP 地址和 MTU
func ConfigureTapInterface(ifName, ip string) error {
	return ipconfig.ConfigureTapInterface(ifName, ip)
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
// 如果不存在，则认领同名 TAP 适配器或创建新的
func EnsureSoGameAdapter() (TapInstallStatus, error) {
	resolved, resolveErr := tapadapter.ResolveKnownAdapter(SoGameAdapterName)
	if resolveErr != nil {
		logger.Warnf("解析已知 TAP 适配器失败，将继续使用旧流程: %v", resolveErr)
	} else if resolved.Status == tapadapter.ResolveFound && resolved.Info != nil {
		logger.Infof("已通过 GUID 找到 SoGame 专属适配器 '%s'", resolved.Info.FriendlyName)
		restartTapInterface(resolved.Info.FriendlyName)
		rememberKnownTapAdapter(resolved)
		return TapAlreadyInstalled, nil
	} else if shouldForgetKnownAdapter(resolved.Status) {
		forgetKnownTapAdapter(resolved.Status)
	}

	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 已存在", SoGameAdapterName)
		restartTapInterface(SoGameAdapterName)
		rememberCurrentSoGameAdapter()
		return TapAlreadyInstalled, nil
	}

	logger.Infof("未找到可认领的 SoGame 专属适配器 '%s'，将创建新 TAP 实例", SoGameAdapterName)

	// 如果没有 TAP 驱动，先安装驱动
	if !isTapDriverInstalled() {
		status, err := installTapDriver()
		if err != nil {
			return status, err
		}
	}

	// 创建新的 TAP 适配器实例并重命名
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

	tapDir, err := tapadapter.FindDriverDir(baseDir, wd)
	if err != nil {
		return TapInstallFailed, err
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

	tapDir, err := tapadapter.FindDriverDir(baseDir, wd)
	if err != nil {
		return TapInstallFailed, err
	}

	infPath := filepath.Join(tapDir, "OemWin2k.inf")

	tapinstallPath, err := tapadapter.FindTapinstall(tapDir)
	if err != nil {
		return TapInstallFailed, err
	}

	logger.Infof("  创建 TAP 适配器实例: %s", tapinstallPath)
	before, err := tapadapter.ListWindowsAdapters()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("获取 TAP 安装前快照失败: %w", err)
	}

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

	after, err := tapadapter.ListWindowsAdapters()
	if err != nil {
		return TapInstallFailed, fmt.Errorf("获取 TAP 安装后快照失败: %w", err)
	}
	created, err := tapadapter.FindNewWindowsAdapter(before, after)
	if err != nil {
		return TapInstallFailed, err
	}

	logger.Infof("  新建 TAP 适配器: %s (LUID=%d)", created.FriendlyName, created.Luid)
	if err := tapadapter.RenameAdapter(created.Luid, SoGameAdapterName); err != nil {
		return TapInstallFailed, fmt.Errorf("重命名新建 TAP 适配器失败: %w", err)
	}
	logger.Infof("  TAP 适配器已重命名为 '%s'", SoGameAdapterName)

	// 启用适配器并设置跃点数
	restartTapInterface(SoGameAdapterName)

	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 创建成功", SoGameAdapterName)
		rememberCurrentSoGameAdapter()
		return TapInstallSuccess, nil
	}

	logger.Warnf("SoGame 专属适配器创建完成但验证未通过，将尝试继续连接")
	return TapInstallSuccess, nil
}

// InstallTapAdapter 兼容旧接口：确保 SoGame 专属适配器存在
func InstallTapAdapter() (TapInstallStatus, error) {
	return EnsureSoGameAdapter()
}

func rememberKnownTapAdapter(resolved tapadapter.ResolveResult) {
	if resolved.Info == nil || resolved.NetCfgInstanceID == "" {
		return
	}
	if err := tapadapter.RememberKnownAdapter(*resolved.Info, resolved.NetCfgInstanceID); err != nil {
		logger.Warnf("保存已知 TAP 适配器失败: %v", err)
	}
}

func restartTapInterface(ifName string) {
	if err := tapadapter.RestartAdapterByName(ifName); err != nil {
		logger.Warnf("重启 TAP 适配器 '%s' 失败: %v", ifName, err)
		return
	}
	logger.Infof("TAP 适配器 '%s' 已重启", ifName)
}

func rememberCurrentSoGameAdapter() {
	if _, err := tapadapter.RememberKnownAdapterByFriendlyName(SoGameAdapterName); err != nil {
		logger.Warnf("保存当前 SoGame TAP 适配器失败: %v", err)
	}
}

func forgetKnownTapAdapter(status tapadapter.ResolveStatus) {
	if err := tapadapter.DeleteKnownAdapter(); err != nil {
		logger.Warnf("删除已知 TAP 适配器记录失败: %v", err)
		return
	}
	logger.Infof("已清理失效 TAP 适配器记录: %s", status)
}

func shouldForgetKnownAdapter(status tapadapter.ResolveStatus) bool {
	switch status {
	case tapadapter.ResolveMissing, tapadapter.ResolveInvalid, tapadapter.ResolveNameMismatch:
		return true
	default:
		return false
	}
}
