//go:build windows

package tap

import (
	"fmt"

	"sogame/internal/logger"

	"golang.org/x/sys/windows"
)

// setupapiNetClassGUID 是 Net 设备类的 GUID（{4d36e972-e325-11ce-bfc1-08002be10318}）。
var setupapiNetClassGUID = &windows.GUID{
	Data1: 0x4d36e972,
	Data2: 0xe325,
	Data3: 0x11ce,
	Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18},
}

// tapRootHardwareID 是 TAP-Windows 的 root-enumerated 硬件 ID。
// 对应 OemVista.inf 中的 "root\tap0901" 硬件 ID 条目。
const tapRootHardwareID = "root\\tap0901"

// CreateAdapterViaSetupAPI 通过 Windows SetupAPI 创建新的 TAP-Windows 适配器实例。
// 该函数替代 tapinstall.exe/devcon.exe，在进程内直接调用 SetupAPI 完成设备创建。
//
// 前置条件：驱动必须已通过 pnputil /add-driver 加入 Windows 驱动存储。
// 原因：BuildDriverInfoList(SPDIT_COMPATDRIVER) 只搜索驱动存储中的驱动。
//
// 流程等价于 devcon install OemVista.inf tap0901：
//  1. 创建设备信息集
//  2. 创建 root-enumerated 设备节点
//  3. 设置硬件 ID（root\tap0901）
//  4. 注册设备到 PnP 管理器
//  5. 构建兼容驱动列表
//  6. 选中 TAP-Windows 驱动
//  7. 安装设备文件
//  8. 注册 co-installer（非致命）
//  9. 安装接口（非致命）
// 10. 安装设备（核心步骤）
func CreateAdapterViaSetupAPI() error {
	// 步骤 1: 创建空的设备信息集
	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(setupapiNetClassGUID, 0, "")
	if err != nil {
		return fmt.Errorf("SetupDiCreateDeviceInfoListEx: %w", err)
	}
	defer devInfo.Close()

	// 步骤 2: 创建 root-enumerated 设备节点
	// DICD_GENERATE_ID 让系统自动生成设备实例 ID（如 ROOT\*tap0901\0000）
	devInfoData, err := devInfo.CreateDeviceInfo("SoGame", setupapiNetClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		return fmt.Errorf("CreateDeviceInfo: %w", err)
	}

	// 步骤 3: 设置硬件 ID
	// 这使 BuildDriverInfoList 能匹配驱动存储中的 tap0901 驱动
	hwIDBytes := encodeMultiSZ(tapRootHardwareID)
	if err := devInfo.SetDeviceRegistryProperty(devInfoData, windows.SPDRP_HARDWAREID, hwIDBytes); err != nil {
		return fmt.Errorf("SetDeviceRegistryProperty(HARDWAREID): %w", err)
	}

	// 步骤 4: 注册设备到 PnP 管理器
	if err := devInfo.CallClassInstaller(windows.DIF_REGISTERDEVICE, devInfoData); err != nil {
		return fmt.Errorf("DIF_REGISTERDEVICE: %w", err)
	}

	// 步骤 5: 构建兼容驱动列表（搜索驱动存储）
	if err := devInfo.BuildDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER); err != nil {
		return fmt.Errorf("BuildDriverInfoList: %w", err)
	}
	defer devInfo.DestroyDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER)

	// 步骤 6: 查找并选中 TAP-Windows 驱动
	drvInfoData, err := findTapCompatibleDriver(devInfo, devInfoData)
	if err != nil {
		return err
	}
	if err := devInfo.SetSelectedDriver(devInfoData, drvInfoData); err != nil {
		return fmt.Errorf("SetSelectedDriver: %w", err)
	}
	logger.Infof("  SetupAPI: 选中驱动 %s", drvInfoData.Description())

	// 步骤 7: 安装设备文件
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLDEVICEFILES, devInfoData); err != nil {
		return fmt.Errorf("DIF_INSTALLDEVICEFILES: %w", err)
	}

	// 步骤 8: 注册 co-installer（非致命，部分驱动无 co-installer）
	if err := devInfo.CallClassInstaller(windows.DIF_REGISTER_COINSTALLERS, devInfoData); err != nil {
		logger.Warnf("  SetupAPI: DIF_REGISTER_COINSTALLERS: %v (非致命)", err)
	}

	// 步骤 9: 安装接口（非致命，部分驱动无设备接口）
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLINTERFACES, devInfoData); err != nil {
		logger.Warnf("  SetupAPI: DIF_INSTALLINTERFACES: %v (非致命)", err)
	}

	// 步骤 10: 安装设备（核心步骤，触发驱动加载和网卡枚举）
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLDEVICE, devInfoData); err != nil {
		return fmt.Errorf("DIF_INSTALLDEVICE: %w", err)
	}

	logger.Infof("  SetupAPI: TAP 设备创建完成")
	return nil
}

// findTapCompatibleDriver 在已构建的驱动列表中查找 TAP-Windows 驱动。
// BuildDriverInfoList 已按驱动排名排序，取第一个 TAP-Windows 驱动即可。
func findTapCompatibleDriver(devInfo windows.DevInfo, devInfoData *windows.DevInfoData) (*windows.DrvInfoData, error) {
	for i := 0; ; i++ {
		drvInfoData, err := devInfo.EnumDriverInfo(devInfoData, windows.SPDIT_COMPATDRIVER, i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}
		desc := drvInfoData.Description()
		if IsWindowsDescription(desc) {
			return drvInfoData, nil
		}
	}
	return nil, fmt.Errorf("未在驱动存储中找到 TAP-Windows 驱动（请先运行 pnputil /add-driver）")
}

// encodeMultiSZ 将字符串编码为 UTF-16LE REG_MULTI_SZ 格式（双 null 终止）。
// SPDRP_HARDWAREID 要求 REG_MULTI_SZ 格式。
func encodeMultiSZ(s string) []byte {
	wide, _ := windows.UTF16FromString(s) // 包含 1 个 null 终止符
	wide = append(wide, 0)                // 追加额外 null 表示 MULTI_SZ 结束
	buf := make([]byte, len(wide)*2)
	for i, v := range wide {
		buf[i*2] = byte(v)
		buf[i*2+1] = byte(v >> 8)
	}
	return buf
}
