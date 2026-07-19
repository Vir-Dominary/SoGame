//go:build windows

package tap

import (
	"fmt"
	"strconv"
	"strings"

	"sogame/internal/logger"

	"golang.org/x/sys/windows"
)

// BundledDriverVersion 是随程序分发的 TAP 驱动版本号。
// 对应 OemVista.inf 中 DriverVer = 02/27/2024,9.27.0.0 的版本部分。
const BundledDriverVersion = "9.27.0.0"

// TapDriverStatus 包含 TAP 驱动版本检测结果。
type TapDriverStatus struct {
	AdapterExists    bool   `json:"adapterExists"`
	InstalledVersion string `json:"installedVersion"`
	BundledVersion   string `json:"bundledVersion"`
	NeedsUpgrade     bool   `json:"needsUpgrade"`
}

// CheckTapDriverStatus 检测当前系统中已安装的 TAP 驱动版本，并与随程序分发的版本比较。
// 如果系统中没有 TAP 适配器，返回 AdapterExists=false。
// 如果有 TAP 适配器但无法读取驱动版本，返回 InstalledVersion 为空且 NeedsUpgrade=false。
func CheckTapDriverStatus() (*TapDriverStatus, error) {
	status := &TapDriverStatus{
		BundledVersion: BundledDriverVersion,
	}

	exists, err := HasWindowsAdapter()
	if err != nil {
		return nil, fmt.Errorf("查询 TAP 适配器失败: %w", err)
	}
	status.AdapterExists = exists
	if !exists {
		return status, nil
	}

	installedVer, err := getInstalledTapDriverVersion()
	if err != nil {
		logger.Warnf("获取已安装 TAP 驱动版本失败，跳过升级检查: %v", err)
		return status, nil
	}
	status.InstalledVersion = installedVer
	status.NeedsUpgrade = compareVersionStrings(installedVer, BundledDriverVersion) < 0
	return status, nil
}

// getInstalledTapDriverVersion 通过 SetupAPI 查询已安装的 TAP 驱动版本。
// 在所有 present 的 Net 类设备中查找硬件 ID 包含 tap0901 的设备，
// 然后读取其兼容驱动列表中排名最高的驱动版本。
func getInstalledTapDriverVersion() (string, error) {
	devInfo, err := windows.SetupDiGetClassDevsEx(setupapiNetClassGUID, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return "", fmt.Errorf("SetupDiGetClassDevsEx: %w", err)
	}
	defer devInfo.Close()

	for i := 0; ; i++ {
		devInfoData, err := devInfo.EnumDeviceInfo(i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}

		hwID, err := getDeviceHardwareID(devInfo, devInfoData)
		if err != nil || !strings.Contains(strings.ToLower(hwID), "tap0901") {
			continue
		}

		// 找到 TAP 设备，构建驱动信息列表
		if err := devInfo.BuildDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER); err != nil {
			continue
		}

		// 枚举驱动列表，取所有 TAP-Windows 驱动中版本最高的。
		// SPDIT_COMPATDRIVER 返回驱动存储中所有兼容驱动（可能同时包含旧版和新版），
		// 不能只取第一个匹配项——旧驱动可能排名靠前导致永远返回旧版本号。
		var bestVersion string
		for j := 0; ; j++ {
			drvInfoData, err := devInfo.EnumDriverInfo(devInfoData, windows.SPDIT_COMPATDRIVER, j)
			if err == windows.ERROR_NO_MORE_ITEMS {
				break
			}
			if err != nil {
				continue
			}
			desc := drvInfoData.Description()
			if !IsWindowsDescription(desc) {
				continue
			}
			ver := unpackDriverVersion(drvInfoData.DriverVersion)
			logger.Debugf("  TAP 驱动候选 %d: %s 版本 %s", j, desc, ver)
			if bestVersion == "" || compareVersionStrings(ver, bestVersion) > 0 {
				bestVersion = ver
			}
		}

		devInfo.DestroyDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER)

		if bestVersion != "" {
			return bestVersion, nil
		}
	}

	return "", fmt.Errorf("未找到 TAP 设备的驱动版本信息")
}

// getDeviceHardwareID 读取设备的硬件 ID 属性（SPDRP_HARDWAREID，REG_MULTI_SZ）。
func getDeviceHardwareID(devInfo windows.DevInfo, devInfoData *windows.DevInfoData) (string, error) {
	val, err := devInfo.DeviceRegistryProperty(devInfoData, windows.SPDRP_HARDWAREID)
	if err != nil {
		return "", err
	}
	switch v := val.(type) {
	case string:
		return v, nil
	case []string:
		if len(v) > 0 {
			return v[0], nil
		}
	}
	return "", nil
}

// unpackDriverVersion 将 uint64 类型的 DriverVersion 解包为 "major.minor.build.revision" 字符串。
// Windows 使用 FILE_VERSION 格式打包：
//
//	bits 48-63: major, bits 32-47: minor, bits 16-31: build, bits 0-15: revision
func unpackDriverVersion(v uint64) string {
	major := uint16(v >> 48)
	minor := uint16(v >> 32)
	build := uint16(v >> 16)
	revision := uint16(v)
	return fmt.Sprintf("%d.%d.%d.%d", major, minor, build, revision)
}

// compareVersionStrings 比较两个 "major.minor.build.revision" 格式的版本字符串。
// 返回 -1 表示 a<b，0 表示相等，1 表示 a>b。
func compareVersionStrings(a, b string) int {
	av := parseVersionParts(a)
	bv := parseVersionParts(b)
	for i := 0; i < 4; i++ {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

func parseVersionParts(s string) [4]uint16 {
	var parts [4]uint16
	seg := strings.SplitN(s, ".", 4)
	for i, p := range seg {
		if i >= 4 {
			break
		}
		n, err := strconv.ParseUint(p, 10, 16)
		if err == nil {
			parts[i] = uint16(n)
		}
	}
	return parts
}
