package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"netjoin/internal/logger"
)

// findTapDir 查找 TAP 驱动文件目录
func findTapDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	baseDir := filepath.Dir(exePath)
	wd, _ := os.Getwd()

	candidates := []string{
		filepath.Join(baseDir, "tap"),
		filepath.Join(baseDir, "installer", "tap"),
		filepath.Join(baseDir, "..", "installer", "tap"),
	}
	if wd != "" && wd != baseDir {
		candidates = append(candidates,
			filepath.Join(wd, "tap"),
			filepath.Join(wd, "installer", "tap"),
		)
	}
	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(filepath.Join(abs, "OemWin2k.inf")); err == nil {
			return abs, nil
		}
	}
	logger.Debugf("TAP 驱动目录搜索失败，已搜索: %v", candidates)
	return "", fmt.Errorf("未找到 TAP 驱动文件目录 (OemWin2k.inf)")
}

// isTapinstallSuccess 检查 tapinstall 输出是否包含驱动安装成功标志
func isTapinstallSuccess(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "drivers installed") ||
		strings.Contains(lower, "device node created") ||
		strings.Contains(lower, "install is complete")
}

// EnsureSoGameAdapter 确保 SoGame-VPN 适配器存在
// 五段降级：GUID查找 → 描述改名 → 创建 → 装驱动→重试
func EnsureSoGameAdapter() (TapInstallStatus, error) {
	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 已存在", SoGameAdapterName)
		saveAdapterRecord(getAdapterGUID(SoGameAdapterName), SoGameAdapterName)
		return TapAlreadyInstalled, nil
	}

	// 尝试0：按记录的 GUID 查找（即使被改名也能精确定位）
	if rec := loadAdapterRecord(); rec != nil {
		if a := findAdapterByGUID(rec.GUID); a != nil {
			logger.Infof("通过 GUID 找回适配器 '%s'（曾用名: %s），正在恢复名称...", a.Name, rec.Name)
			renameCmd := exec.Command("netsh", "interface", "set", "interface",
				fmt.Sprintf("name=%s", a.Name), fmt.Sprintf("newname=%s", SoGameAdapterName))
			renameCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if out, err := renameCmd.CombinedOutput(); err != nil {
				logger.Warnf("GUID 适配器重命名失败: %v, %s", err, strings.TrimSpace(string(out)))
			} else {
				logger.Infof("已将 GUID 适配器 '%s' 恢复为 '%s'", a.Name, SoGameAdapterName)
				saveAdapterRecord(rec.GUID, SoGameAdapterName)
				return TapInstallSuccess, nil
			}
		} else {
			logger.Infof("记录的适配器 GUID=%s 已不存在，将重新创建", rec.GUID)
		}
	}

	logger.Infof("正在创建 SoGame 专属 TAP 适配器 '%s'...", SoGameAdapterName)

	// 尝试1：将已有的 TAP 适配器直接改名（不调 tapinstall，避免对驱动重新绑定导致其他 TAP 实例 Code 10）
	if oldName := FindTapAdapterName(); oldName != "" {
		logger.Infof("发现现有 TAP 适配器 '%s'，正在重命名为 '%s'...", oldName, SoGameAdapterName)
		renameCmd := exec.Command("netsh", "interface", "set", "interface",
			fmt.Sprintf("name=%s", oldName), fmt.Sprintf("newname=%s", SoGameAdapterName))
		renameCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if out, err := renameCmd.CombinedOutput(); err != nil {
			logger.Warnf("netsh 重命名适配器失败: %v, %s", err, strings.TrimSpace(string(out)))
		} else {
			logger.Infof("已将 '%s' 重命名为 '%s'", oldName, SoGameAdapterName)
			saveAdapterRecord(getAdapterGUID(SoGameAdapterName), SoGameAdapterName)
			return TapInstallSuccess, nil
		}
	}

	// 尝试2：创建新实例（此时系统中没有任何 TAP 适配器）
	status, err := createSoGameAdapter()
	if err == nil {
		saveAdapterRecord(getAdapterGUID(SoGameAdapterName), SoGameAdapterName)
		return status, nil
	}

	// 尝试3：tapinstall 失败，可能是驱动未安装
	logger.Infof("tapinstall 失败，尝试安装 TAP 驱动...")
	if _, drvErr := installTapDriver(); drvErr != nil {
		return TapInstallFailed, fmt.Errorf("TAP 驱动安装失败: %w (tapinstall 失败: %v)", drvErr, err)
	}
	logger.Infof("驱动安装完成，重试创建 TAP 适配器实例...")
	status, err = createSoGameAdapter()
	if err != nil {
		logger.Errorf("驱动已安装但创建 TAP 适配器仍然失败: %v", err)
	} else {
		saveAdapterRecord(getAdapterGUID(SoGameAdapterName), SoGameAdapterName)
	}
	return status, err
}

// installTapDriver 安装 TAP 驱动到系统驱动存储
func installTapDriver() (TapInstallStatus, error) {
	tapDir, err := findTapDir()
	if err != nil {
		return TapInstallFailed, err
	}

	logger.Infof("正在安装 TAP 驱动，驱动目录: %s", tapDir)
	infPath := filepath.Join(tapDir, "OemWin2k.inf")

	logger.Infof("  添加 TAP 驱动到驱动存储...")
	pnputilCmd := exec.Command("pnputil", "/add-driver", infPath, "/install")
	pnputilCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	pnputilOutput, pnputilErr := pnputilCmd.CombinedOutput()
	outputStr := strings.ToLower(string(pnputilOutput))

	// 驱动已存在于驱动存储（输出含 oemNNN.inf），视为成功
	if pnputilErr == nil || strings.Contains(outputStr, "oem") {
		if pnputilErr != nil {
			logger.Infof("  TAP 驱动已存在于驱动存储")
		} else {
			logger.Infof("  pnputil /add-driver 成功")
		}
		return TapInstallSuccess, nil
	}

	logger.Warnf("  pnputil /add-driver 失败: %v, 输出: %s", pnputilErr, strings.TrimSpace(string(pnputilOutput)))
	return TapInstallFailed, fmt.Errorf("pnputil /add-driver 失败: %v", pnputilErr)
}

// createSoGameAdapter 创建 TAP 适配器实例并重命名
func createSoGameAdapter() (TapInstallStatus, error) {
	tapDir, err := findTapDir()
	if err != nil {
		return TapInstallFailed, err
	}

	infPath := filepath.Join(tapDir, "OemWin2k.inf")

	tapinstallCandidates := []string{
		filepath.Join(tapDir, "tapinstall.exe"),
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

	// 记录安装前的网卡快照，用于精确定位新创建的适配器
	beforeGUIDs := adapterGUIDSet()

	installCmd := exec.Command(tapinstallPath, "install", infPath, "tap0901")
	installCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	installOutput, installErr := installCmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(installOutput))
	success := isTapinstallSuccess(outputStr)

	if !success {
		if installErr != nil {
			logger.Warnf("  tapinstall install 失败: %v, 输出: %s", installErr, outputStr)
		} else {
			logger.Warnf("  tapinstall 返回成功但未检测到安装标志，输出: %s", outputStr)
		}
		logger.Infof("  重试 tapinstall install...")
		time.Sleep(2 * time.Second)

		installCmd2 := exec.Command(tapinstallPath, "install", infPath, "tap0901")
		installCmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		installOutput2, installErr2 := installCmd2.CombinedOutput()
		outputStr2 := strings.TrimSpace(string(installOutput2))

		if !isTapinstallSuccess(outputStr2) && installErr2 != nil {
			return TapInstallFailed, fmt.Errorf("TAP 适配器安装失败: %v\n输出: %s", installErr2, outputStr2)
		}
		logger.Infof("  tapinstall install 重试成功")
	} else if installErr != nil {
		logger.Infof("  tapinstall exit code=%v 但驱动安装成功", installErr)
	} else {
		logger.Infof("  tapinstall install 成功")
	}

	time.Sleep(2 * time.Second)

	// 对比前后快照，精确定位新创建的 TAP 适配器
	newTap := findNewTapAdapter(beforeGUIDs)
	if newTap == "" {
		return TapInstallFailed, fmt.Errorf("TAP 适配器安装后未检测到新设备")
	}

	logger.Infof("  发现新 TAP 适配器 '%s'，正在重命名为 '%s'...", newTap, SoGameAdapterName)
	renameCmd := exec.Command("netsh", "interface", "set", "interface",
		fmt.Sprintf("name=%s", newTap), fmt.Sprintf("newname=%s", SoGameAdapterName))
	renameCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := renameCmd.CombinedOutput(); err != nil {
		return TapInstallFailed, fmt.Errorf("重命名 TAP 适配器失败: %v, %s", err, strings.TrimSpace(string(out)))
	}
	logger.Infof("  TAP 适配器已重命名为 '%s'", SoGameAdapterName)

	if IsSoGameAdapterExists() {
		logger.Infof("SoGame 专属适配器 '%s' 创建成功", SoGameAdapterName)
		return TapInstallSuccess, nil
	}

	return TapInstallFailed, fmt.Errorf("TAP 适配器创建后验证失败：系统中未找到 '%s'", SoGameAdapterName)
}

// adapterGUIDSet 返回安装前所有网卡 GUID 的集合，用于安装前后对比
func adapterGUIDSet() map[string]bool {
	set := make(map[string]bool)
	for _, a := range getAllAdapters() {
		set[a.GUID] = true
	}
	return set
}

// findNewTapAdapter 对比安装前后的网卡 GUID，找出新增的 TAP 适配器
func findNewTapAdapter(before map[string]bool) string {
	for _, a := range getAllAdapters() {
		if before[a.GUID] {
			continue
		}
		if isTapDesc(strings.ToLower(a.Desc)) {
			return a.Name
		}
	}
	return ""
}
