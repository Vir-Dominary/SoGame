//go:build windows

package tap

import (
	"fmt"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 本文件是 Go SetupAPI 创建 TAP 设备方案的测试。
//
// 测试分层：
//   - TestSetupAPIAvailability:     纯单元测试，无需管理员，验证 Go SetupAPI 函数/常量可用
//   - TestSetupAPIDriverStoreQuery: 单元测试，需管理员，验证驱动存储中能找到 tap0901
//   - TestSetupAPICreateTapAdapter: 集成测试，需管理员，用 Go SetupAPI 创建 TAP 设备并清理
//
// 运行集成测试（需以管理员身份运行）：
//   go test -v -run TestSetupAPICreateTapAdapter ./internal/tap/

// TestSetupAPIAvailability 验证 Go 运行时中 SetupAPI 关键函数/常量均可访问。
// 这是纯编译期+运行期符号检查，不触碰系统状态，不需要管理员权限。
func TestSetupAPIAvailability(t *testing.T) {
	t.Run("constants", func(t *testing.T) {
		checks := map[string]uint32{
			"DICD_GENERATE_ID":          uint32(windows.DICD_GENERATE_ID),
			"SPDRP_HARDWAREID":          uint32(windows.SPDRP_HARDWAREID),
			"SPDIT_COMPATDRIVER":        uint32(windows.SPDIT_COMPATDRIVER),
			"DIF_REGISTERDEVICE":        uint32(windows.DIF_REGISTERDEVICE),
			"DIF_INSTALLDEVICEFILES":    uint32(windows.DIF_INSTALLDEVICEFILES),
			"DIF_INSTALLINTERFACES":     uint32(windows.DIF_INSTALLINTERFACES),
			"DIF_INSTALLDEVICE":         uint32(windows.DIF_INSTALLDEVICE),
			"DIF_REGISTER_COINSTALLERS": uint32(windows.DIF_REGISTER_COINSTALLERS),
		}
		for name, val := range checks {
			if val == 0 {
				t.Errorf("常量 %s 值为 0，可能未定义", name)
			} else {
				t.Logf("OK %s = 0x%x", name, val)
			}
		}
	})

	t.Run("create_device_info_list", func(t *testing.T) {
		// 仅验证能创建空的设备信息集，不创建任何设备
		devInfo, err := windows.SetupDiCreateDeviceInfoListEx(setupapiNetClassGUID, 0, "")
		if err != nil {
			t.Fatalf("SetupDiCreateDeviceInfoListEx 失败: %v", err)
		}
		defer devInfo.Close()
		t.Log("OK SetupDiCreateDeviceInfoListEx")
	})
}

// TestSetupAPIDriverStoreQuery 验证驱动存储中已存在 tap0901 驱动。
// 前提：已通过 pnputil /add-driver 安装驱动到驱动存储。
// 注意：CreateDeviceInfo 对 Net 类需要写注册表，因此需要管理员权限。
func TestSetupAPIDriverStoreQuery(t *testing.T) {
	if !isAdmin() {
		t.Skip("SetupAPI 设备节点查询需要管理员权限")
	}
	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(setupapiNetClassGUID, 0, "")
	if err != nil {
		t.Fatalf("SetupDiCreateDeviceInfoListEx: %v", err)
	}
	defer devInfo.Close()

	// 创建一个临时设备节点用于查询兼容驱动（不真正注册）
	devInfoData, err := devInfo.CreateDeviceInfo("SoGameTest", setupapiNetClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		t.Fatalf("CreateDeviceInfo: %v", err)
	}

	// 设置硬件 ID，使 BuildDriverInfoList 能匹配驱动存储中的 tap0901 驱动
	hwIDBytes := encodeMultiSZ(tapRootHardwareID)
	if err := devInfo.SetDeviceRegistryProperty(devInfoData, windows.SPDRP_HARDWAREID, hwIDBytes); err != nil {
		t.Fatalf("SetDeviceRegistryProperty(HARDWAREID): %v", err)
	}

	// 构建兼容驱动列表 —— 这会搜索驱动存储
	if err := devInfo.BuildDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER); err != nil {
		t.Skipf("BuildDriverInfoList 失败（可能驱动未加入驱动存储）: %v", err)
	}
	defer devInfo.DestroyDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER)

	count := 0
	for i := 0; ; i++ {
		drvInfoData, err := devInfo.EnumDriverInfo(devInfoData, windows.SPDIT_COMPATDRIVER, i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			t.Logf("EnumDriverInfo[%d] 错误: %v", i, err)
			continue
		}
		detail, err := devInfo.DriverInfoDetail(devInfoData, drvInfoData)
		if err != nil {
			t.Logf("DriverInfoDetail[%d] 错误: %v", i, err)
			continue
		}
		count++
		t.Logf("驱动[%d]: desc=%s inf=%s", i, drvInfoData.Description(), detail.InfFileName())
	}

	if count == 0 {
		t.Skip("驱动存储中未找到 tap0901 兼容驱动，请先运行 pnputil /add-driver OemVista.inf /install")
	}
	t.Logf("OK 驱动存储中找到 %d 个兼容驱动", count)
}

// TestSetupAPICreateTapAdapter 是 Go SetupAPI 方案的集成测试。
// 它完整模拟 devcon install 的流程：创建设备 → 设置硬件 ID → 注册 → 安装。
//
// 注意：此测试会真实创建 TAP 网卡，需要：
//   1. 管理员权限
//   2. 驱动已通过 pnputil /add-driver 加入驱动存储
func TestSetupAPICreateTapAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	if !isAdmin() {
		t.Skip("需要管理员权限")
	}
	if err := ensureDriverInStore(); err != nil {
		t.Skipf("驱动不在驱动存储中: %v", err)
	}

	beforeCount := countTapAdapters(t)

	if err := CreateAdapterViaSetupAPI(); err != nil {
		t.Fatalf("CreateAdapterViaSetupAPI 失败: %v", err)
	}

	// 等待设备枚举稳定（复用项目经验：6 秒基础等待）
	waitForDeviceRefresh()

	afterCount := countTapAdapters(t)
	if afterCount <= beforeCount {
		t.Errorf("SetupAPI 安装后 TAP 数量未增加: before=%d after=%d", beforeCount, afterCount)
	} else {
		t.Logf("OK: TAP 适配器数量 %d -> %d", beforeCount, afterCount)
	}

	// 清理：移除刚创建的设备
	// 通过 DIF_REMOVE 移除最新创建的 TAP 设备
	removeNewestTapDevice(t)
}

// --- 辅助函数 ---

func isAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// ensureDriverInStore 检查驱动存储中是否已有 tap0901 驱动。
func ensureDriverInStore() error {
	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(setupapiNetClassGUID, 0, "")
	if err != nil {
		return err
	}
	defer devInfo.Close()

	devInfoData, err := devInfo.CreateDeviceInfo("probe", setupapiNetClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		return err
	}
	hwIDBytes := encodeMultiSZ(tapRootHardwareID)
	if err := devInfo.SetDeviceRegistryProperty(devInfoData, windows.SPDRP_HARDWAREID, hwIDBytes); err != nil {
		return err
	}
	if err := devInfo.BuildDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER); err != nil {
		return err
	}
	defer devInfo.DestroyDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER)

	for i := 0; ; i++ {
		_, err := devInfo.EnumDriverInfo(devInfoData, windows.SPDIT_COMPATDRIVER, i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err == nil {
			return nil // 找到至少一个驱动
		}
	}
	return fmt.Errorf("驱动存储中未找到 tap0901")
}

func countTapAdapters(t *testing.T) int {
	adapters, err := ListWindowsAdapters()
	if err != nil {
		t.Logf("countTapAdapters: %v", err)
		return 0
	}
	return len(adapters)
}

// removeNewestTapDevice 移除最新的 TAP 设备（用于测试清理）。
func removeNewestTapDevice(t *testing.T) {
	devInfo, err := windows.SetupDiGetClassDevsEx(setupapiNetClassGUID, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		t.Logf("清理: SetupDiGetClassDevsEx 失败: %v", err)
		return
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

		// 检查是否是 TAP 设备
		desc, err := getDeviceDescription(devInfo, devInfoData)
		if err != nil || !IsWindowsDescription(desc) {
			continue
		}

		// 找到 TAP 设备，尝试移除
		params := &windows.RemoveDeviceParams{
			ClassInstallHeader: *windows.MakeClassInstallHeader(windows.DIF_REMOVE),
			Scope:              windows.DI_REMOVEDEVICE_GLOBAL,
			HwProfile:          0,
		}
		if err := devInfo.SetClassInstallParams(devInfoData, &params.ClassInstallHeader, uint32(unsafe.Sizeof(*params))); err != nil {
			t.Logf("清理: SetClassInstallParams(DIF_REMOVE) 失败: %v", err)
			return
		}
		if err := devInfo.CallClassInstaller(windows.DIF_REMOVE, devInfoData); err != nil {
			t.Logf("清理: CallClassInstaller(DIF_REMOVE) 失败: %v", err)
			return
		}
		t.Logf("清理 OK: TAP 设备已移除")
		return
	}
	t.Logf("清理: 未找到可移除的 TAP 设备")
}

// getDeviceDescription 读取设备的 DeviceDesc 属性。
func getDeviceDescription(devInfo windows.DevInfo, devInfoData *windows.DevInfoData) (string, error) {
	val, err := devInfo.DeviceRegistryProperty(devInfoData, windows.SPDRP_DEVICEDESC)
	if err != nil {
		return "", err
	}
	switch s := val.(type) {
	case string:
		return s, nil
	case []string:
		if len(s) > 0 {
			return s[0], nil
		}
	}
	return fmt.Sprintf("%v", val), nil
}

func waitForDeviceRefresh() {
	time.Sleep(6 * time.Second)
}
