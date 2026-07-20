//go:build windows

package tap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"sogame/internal/logger"

	"golang.org/x/sys/windows"
)

// RemoveAllTapAdapters 移除系统中所有 present 的 TAP-Windows 适配器实例。
// 用于驱动升级流程：先移除绑定旧驱动的设备实例，再安装新驱动并创建新实例。
func RemoveAllTapAdapters() error {
	devInfo, err := windows.SetupDiGetClassDevsEx(setupapiNetClassGUID, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return fmt.Errorf("SetupDiGetClassDevsEx: %w", err)
	}
	defer devInfo.Close()

	// 先收集所有 TAP 设备，避免在枚举过程中修改设备列表导致索引错乱
	var tapDevices []*windows.DevInfoData
	for i := 0; ; i++ {
		devInfoData, err := devInfo.EnumDeviceInfo(i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}
		hwID, err := getDeviceHardwareID(devInfo, devInfoData)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(hwID), "tap0901") {
			tapDevices = append(tapDevices, devInfoData)
		}
	}

	if len(tapDevices) == 0 {
		logger.Infof("未找到需要移除的 TAP 适配器实例")
		return nil
	}

	logger.Infof("发现 %d 个 TAP 适配器实例，开始移除...", len(tapDevices))
	removed := 0
	for _, devInfoData := range tapDevices {
		if err := removeRegisteredDevice(devInfo, devInfoData); err != nil {
			logger.Warnf("移除 TAP 设备失败: %v", err)
			continue
		}
		removed++
	}
	logger.Infof("已移除 %d/%d 个 TAP 适配器实例", removed, len(tapDevices))
	return nil
}

// InstallDriverToStore 将随程序分发的 TAP 驱动安装到 Windows 驱动存储。
// 使用 pnputil /add-driver /install，会自动替换同硬件 ID 的旧版本驱动。
func InstallDriverToStore() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}
	baseDir := filepath.Dir(exePath)
	wd, _ := os.Getwd()

	tapDir, err := FindDriverDir(baseDir, wd)
	if err != nil {
		return fmt.Errorf("查找 TAP 驱动目录失败: %w", err)
	}

	infPath := filepath.Join(tapDir, "OemVista.inf")
	logger.Infof("正在安装 TAP 驱动到驱动存储: %s", infPath)

	cmd := exec.Command("pnputil", "/add-driver", infPath, "/install")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pnputil /add-driver 失败: %w, 输出: %s", err, strings.TrimSpace(string(output)))
	}
	logger.Infof("TAP 驱动安装到驱动存储成功")
	return nil
}

// UpgradeTapDriver 执行完整的 TAP 驱动升级流程：
//  1. 先安装新驱动到驱动存储（不影响现有适配器，失败时系统仍可正常使用旧适配器）
//  2. 移除所有现有 TAP-Windows 适配器实例（解绑旧驱动）
//  3. 通过 SetupAPI 创建新适配器实例（自动绑定新驱动）
//
// 升级后新适配器的友好名仍为默认名（如 "TAP-Windows Adapter V9"），
// 调用方应随后调用 platform.EnsureSoGameAdapter() 完成重命名和配置。
func UpgradeTapDriver() error {
	logger.Infof("开始 TAP 驱动升级流程")

	// 步骤 1：先安装新驱动到驱动存储。
	// 此操作仅向驱动库添加新版本，不会影响已存在的适配器实例，
	// 即使失败也不会破坏现有网络环境。
	if err := InstallDriverToStore(); err != nil {
		return fmt.Errorf("安装新 TAP 驱动失败: %w", err)
	}

	// 步骤 2：移除所有现有 TAP 适配器实例，解绑旧驱动。
	if err := RemoveAllTapAdapters(); err != nil {
		return fmt.Errorf("移除旧 TAP 适配器失败: %w", err)
	}

	// 步骤 3：创建新适配器实例，将自动绑定已安装的最新驱动。
	if err := CreateAdapterViaSetupAPI(); err != nil {
		return fmt.Errorf("创建新 TAP 适配器失败: %w", err)
	}

	logger.Infof("TAP 驱动升级流程完成")
	return nil
}
