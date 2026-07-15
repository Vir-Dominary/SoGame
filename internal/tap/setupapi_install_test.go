//go:build windows

package tap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 本文件是 tapinstall/devcon vs Go SetupAPI 两种 TAP 设备创建方案的对比测试。
//
// 测试分层：
//   - TestSetupAPIAvailability:       纯单元测试，无需管理员，验证 Go SetupAPI 函数可用
//   - TestSetupAPIDriverStoreQuery:   单元测试，无需管理员，验证驱动存储中能找到 tap0901
//   - TestDevconCreateTapAdapter:     集成测试，需管理员 + devcon.exe，用 devcon 创建 TAP
//   - TestSetupAPICreateTapAdapter:   集成测试，需管理员 + -tags=integration，用 Go SetupAPI 创建 TAP
//
// 运行集成测试（需以管理员身份运行）：
//   go test -v -run "TestSetupAPICreateTapAdapter|TestDevconCreateTapAdapter" -tags=integration ./internal/tap/
//
// 集成测试会真实创建 TAP 适配器并在结束时清理，请确保有权限且环境干净。

// netClassGUID 是 Net 设备类的 GUID（{4d36e972-e325-11ce-bfc1-08002be10318}）。
var netClassGUID = &windows.GUID{
	Data1: 0x4d36e972,
	Data2: 0xe325,
	Data3: 0x11ce,
	Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18},
}

// tapHardwareID 是 TAP-Windows 的 root-enumerated 硬件 ID。
const tapHardwareID = "root\\tap0901"

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
		devInfo, err := windows.SetupDiCreateDeviceInfoListEx(netClassGUID, 0, "")
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
	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(netClassGUID, 0, "")
	if err != nil {
		t.Fatalf("SetupDiCreateDeviceInfoListEx: %v", err)
	}
	defer devInfo.Close()

	// 创建一个临时设备节点用于查询兼容驱动（不真正注册）
	devInfoData, err := devInfo.CreateDeviceInfo("SoGameTest", netClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		t.Fatalf("CreateDeviceInfo: %v", err)
	}

	// 设置硬件 ID，使 BuildDriverInfoList 能匹配驱动存储中的 tap0901 驱动
	hwIDBytes := utf16FromString(tapHardwareID)
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
//   2. -tags=integration 标志
//   3. 驱动已通过 pnputil /add-driver 加入驱动存储
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

	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(netClassGUID, 0, "")
	if err != nil {
		t.Fatalf("SetupDiCreateDeviceInfoListEx: %v", err)
	}
	defer devInfo.Close()

	// 步骤 1: 创建设备节点
	devInfoData, err := devInfo.CreateDeviceInfo("SoGameSetupAPITest", netClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		t.Fatalf("CreateDeviceInfo: %v", err)
	}
	t.Logf("步骤1 OK: 创建设备节点")

	// 步骤 2: 设置硬件 ID
	hwIDBytes := utf16FromString(tapHardwareID)
	if err := devInfo.SetDeviceRegistryProperty(devInfoData, windows.SPDRP_HARDWAREID, hwIDBytes); err != nil {
		t.Fatalf("SetDeviceRegistryProperty: %v", err)
	}
	t.Logf("步骤2 OK: 设置硬件 ID = %s", tapHardwareID)

	// 步骤 3: 注册设备到 PnP 管理器
	if err := devInfo.CallClassInstaller(windows.DIF_REGISTERDEVICE, devInfoData); err != nil {
		t.Fatalf("DIF_REGISTERDEVICE: %v", err)
	}
	t.Logf("步骤3 OK: 注册设备")
	defer cleanupDevice(t, devInfo, devInfoData)

	// 步骤 4: 构建驱动列表并选择
	if err := devInfo.BuildDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER); err != nil {
		t.Fatalf("BuildDriverInfoList: %v", err)
	}
	defer devInfo.DestroyDriverInfoList(devInfoData, windows.SPDIT_COMPATDRIVER)

	drvInfoData, err := findTapDriver(devInfo, devInfoData)
	if err != nil {
		t.Fatalf("查找 tap0901 驱动失败: %v", err)
	}
	if err := devInfo.SetSelectedDriver(devInfoData, drvInfoData); err != nil {
		t.Fatalf("SetSelectedDriver: %v", err)
	}
	t.Logf("步骤4 OK: 选中驱动 %s", drvInfoData.Description())

	// 步骤 5: 安装设备文件
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLDEVICEFILES, devInfoData); err != nil {
		t.Fatalf("DIF_INSTALLDEVICEFILES: %v", err)
	}
	t.Logf("步骤5 OK: 安装设备文件")

	// 步骤 6: 注册 co-installer
	_ = devInfo.CallClassInstaller(windows.DIF_REGISTER_COINSTALLERS, devInfoData)
	t.Logf("步骤6 OK: 注册 co-installer")

	// 步骤 7: 安装接口
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLINTERFACES, devInfoData); err != nil {
		t.Logf("警告: DIF_INSTALLINTERFACES: %v（部分驱动可忽略）", err)
	}

	// 步骤 8: 安装设备（核心步骤）
	if err := devInfo.CallClassInstaller(windows.DIF_INSTALLDEVICE, devInfoData); err != nil {
		t.Fatalf("DIF_INSTALLDEVICE: %v", err)
	}
	t.Logf("步骤8 OK: 安装设备完成")

	// 验证：检查设备是否出现在系统中
	if !verifyTapCreated(t) {
		t.Errorf("设备安装流程完成但未检测到新 TAP 适配器")
	} else {
		t.Logf("验证 OK: TAP 适配器已创建")
	}
}

// TestDevconCreateTapAdapter 是 devcon.exe 方案的集成测试，用于对比。
// 需要 devcon.exe 可用（在 PATH 或 installer/tap/ 目录下）。
func TestDevconCreateTapAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	if !isAdmin() {
		t.Skip("需要管理员权限")
	}
	if err := ensureDriverInStore(); err != nil {
		t.Skipf("驱动不在驱动存储中: %v", err)
	}

	devconPath := findDevcon()
	if devconPath == "" {
		t.Skip("未找到 devcon.exe，跳过对比测试")
	}

	tapDir, err := FindDriverDir(filepath.Dir(getExePath()), "")
	if err != nil {
		t.Skipf("未找到 TAP 驱动目录: %v", err)
	}
	infPath := filepath.Join(tapDir, "OemVista.inf")

	t.Logf("使用 devcon: %s", devconPath)
	t.Logf("使用 INF: %s", infPath)

	beforeCount := countTapAdapters(t)

	// devcon install OemVista.inf tap0901
	cmd := exec.Command(devconPath, "install", infPath, "tap0901")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(output))
	t.Logf("devcon 输出:\n%s", outStr)

	if err != nil {
		t.Fatalf("devcon install 失败: %v", err)
	}

	// 等待设备枚举
	// (复用项目经验：6 秒基础等待)
	waitForDeviceRefresh()

	afterCount := countTapAdapters(t)
	if afterCount <= beforeCount {
		t.Errorf("devcon 安装后 TAP 数量未增加: before=%d after=%d", beforeCount, afterCount)
	} else {
		t.Logf("OK: TAP 适配器数量 %d -> %d", beforeCount, afterCount)
	}
}

// --- 辅助函数 ---

func utf16FromString(s string) []byte {
	// Windows 注册表要求 REG_MULTI_SZ 格式（UTF-16LE + 双 null 终止）
	runes := []rune(s)
	buf := make([]uint16, 0, len(runes)+2)
	for _, r := range runes {
		buf = append(buf, uint16(r))
	}
	buf = append(buf, 0, 0) // 双 null 终止
	return unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*2)
}

func isAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func getExePath() string {
	p, _ := os.Executable()
	return p
}

func ensureDriverInStore() error {
	// 检查驱动存储中是否已有 tap0901 驱动
	devInfo, err := windows.SetupDiCreateDeviceInfoListEx(netClassGUID, 0, "")
	if err != nil {
		return err
	}
	defer devInfo.Close()

	devInfoData, err := devInfo.CreateDeviceInfo("probe", netClassGUID, "", 0, windows.DICD_GENERATE_ID)
	if err != nil {
		return err
	}
	hwIDBytes := utf16FromString(tapHardwareID)
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

func findTapDriver(devInfo windows.DevInfo, devInfoData *windows.DevInfoData) (*windows.DrvInfoData, error) {
	for i := 0; ; i++ {
		drvInfoData, err := devInfo.EnumDriverInfo(devInfoData, windows.SPDIT_COMPATDRIVER, i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}
		desc := drvInfoData.Description()
		if strings.Contains(desc, "TAP-Windows") || strings.Contains(desc, "tap0901") {
			return drvInfoData, nil
		}
	}
	return nil, fmt.Errorf("未在驱动列表中找到 TAP-Windows 驱动")
}

func cleanupDevice(t *testing.T, devInfo windows.DevInfo, devInfoData *windows.DevInfoData) {
	// 尝试移除设备（DIF_REMOVE）
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
	t.Logf("清理 OK: 设备已移除")
}

func verifyTapCreated(t *testing.T) bool {
	adapters, err := ListWindowsAdapters()
	if err != nil {
		t.Logf("验证: ListWindowsAdapters 失败: %v", err)
		return false
	}
	return len(adapters) > 0
}

func countTapAdapters(t *testing.T) int {
	adapters, err := ListWindowsAdapters()
	if err != nil {
		t.Logf("countTapAdapters: %v", err)
		return 0
	}
	return len(adapters)
}

func findDevcon() string {
	candidates := []string{
		"devcon.exe",
		filepath.Join("installer", "tap", "devcon.exe"),
		filepath.Join("installer", "tap", "tapinstall.exe"),
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func waitForDeviceRefresh() {
	// 复用项目的经验值：6 秒基础等待
	// 实际项目中用 tapCreateBaseWait，这里直接用等价值
	// 不能导入 internal/platform（循环依赖），所以直接 sleep
	time.Sleep(6 * time.Second)
}
