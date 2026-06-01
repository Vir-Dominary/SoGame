package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetAllAdapters(t *testing.T) {
	adapters, err := GetAllAdapters()
	if err != nil {
		t.Fatalf("GetAllAdapters failed: %v", err)
	}
	if len(adapters) == 0 {
		t.Fatal("expected at least one network adapter, got 0")
	}
	t.Logf("found %d adapter(s):", len(adapters))
	for _, a := range adapters {
		t.Logf("  [%s] %-40s GUID=%-40s desc=%s", a.Status, a.Name, a.GUID, a.Desc)
	}
}

func TestAdapterExists(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters available")
	}
	first := adapters[0]
	if !AdapterExists(first.Name) {
		t.Errorf("AdapterExists(%q) returned false, expected true", first.Name)
	}
	if AdapterExists("__nonexistent_adapter__") {
		t.Error("AdapterExists for nonexistent adapter returned true")
	}
}

func TestFindAdapterByDesc(t *testing.T) {
	name := FindAdapterByDesc("*Loopback*")
	if name == "" {
		name = FindAdapterByDesc("*loopback*")
	}
	t.Logf("loopback adapter name: %q", name)
	if name == "" {
		t.Error("expected to find a loopback adapter")
	}
}

func TestTapDetection(t *testing.T) {
	exists := TapAdapterExists()
	t.Logf("TAP adapter exists: %v", exists)
	t.Logf("SoGame adapter exists: %v", AdapterExists(SoGameAdapterName))
	t.Logf("FindTapAdapterName: %q", FindTapAdapterName())
}

func TestAdapterStatus(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters")
	}
	for _, a := range adapters {
		status := AdapterStatus(a.Name)
		up := IsAdapterUp(a.Name)
		if status != a.Status {
			t.Errorf("AdapterStatus(%s) = %s, expected %s", a.Name, status, a.Status)
		}
		expectedUp := a.Status == "Up"
		if up != expectedUp {
			t.Errorf("IsAdapterUp(%s) = %v, expected %v (status=%s)", a.Name, up, expectedUp, a.Status)
		}
	}
}

func TestNoCrashOnRapidCalls(t *testing.T) {
	for i := 0; i < 50; i++ {
		AdapterExists(SoGameAdapterName)
		TapAdapterExists()
		FindTapAdapterName()
		IsAdapterUp("以太网")
	}
}

func TestOperStatusString(t *testing.T) {
	cases := map[uint32]string{
		1: "Up",
		2: "Down",
		3: "Testing",
		4: "Unknown",
		5: "Dormant",
		6: "NotPresent",
		7: "LowerLayerDown",
		0: "Unknown(0)",
		8: "Unknown(8)",
	}
	for code, expected := range cases {
		got := operStatusString(code)
		if got != expected {
			t.Errorf("operStatusString(%d) = %q, expected %q", code, got, expected)
		}
	}
}

func TestFormatMAC(t *testing.T) {
	cases := []struct {
		input    []byte
		expected string
	}{
		{nil, ""},
		{[]byte{}, ""},
		{[]byte{0x00, 0xff, 0x20, 0x3c, 0xf2, 0x45}, "00:ff:20:3c:f2:45"},
		{[]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, "aa:bb:cc:dd:ee:ff"},
	}
	for _, c := range cases {
		got := formatMAC(c.input)
		if got != c.expected {
			t.Errorf("formatMAC(%v) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestMatchSimplePattern(t *testing.T) {
	cases := []struct {
		pattern, s string
		expected   bool
	}{
		{"", "", true},
		{"", "a", false},
		{"*", "", true},
		{"*", "anything", true},
		{"*loopback*", "Software Loopback Interface 1", true},
		{"*LOOPBACK*", "Software Loopback Interface 1", true},
		{"*tap*", "TAP-Windows Adapter V9", true},
		{"*nonexistent*", "TAP-Windows Adapter V9", false},
		{"TAP*", "TAP-Windows Adapter V9", true},
		{"*V9", "TAP-Windows Adapter V9", true},
		{"TAP*V9", "TAP-Windows Adapter V9", true},
		{"TAP*V8", "TAP-Windows Adapter V9", false},
		{"*Wi-Fi*", "MediaTek Wi-Fi 6 MT7921 Wireless LAN Card", true},
		{"NoWildcard", "NoWildcard", true},
		{"NoWildcard", "nowildcard", true},
		{"CaseSensitive", "casesensitive", true},
	}
	for _, c := range cases {
		got := matchSimplePattern(c.pattern, c.s)
		if got != c.expected {
			t.Errorf("matchSimplePattern(%q, %q) = %v, expected %v", c.pattern, c.s, got, c.expected)
		}
	}
}

func TestIsTapinstallSuccess(t *testing.T) {
	cases := map[string]bool{
		"Device node created. Install is complete when drivers are installed...\nDrivers installed successfully.": true,
		"drivers installed":             true,
		"device node created":           true,
		"Install is complete":           true,
		"failed to install driver":      false,
		"":                              false,
		"some random output":            false,
	}
	for output, expected := range cases {
		got := isTapinstallSuccess(output)
		if got != expected {
			t.Errorf("isTapinstallSuccess(%q) = %v, expected %v", output, got, expected)
		}
	}
}

func TestToLowerASCII(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"TAP-Windows":                       "tap-windows",
		"tap0901":                           "tap0901",
		"MixedCase":                         "mixedcase",
		"UPPERCASE":                         "uppercase",
		"lowercase":                         "lowercase",
		"123!@#":                            "123!@#",
		"TAP-WINDOWS ADAPTER V9":            "tap-windows adapter v9",
	}
	for input, expected := range cases {
		got := toLowerASCII(input)
		if got != expected {
			t.Errorf("toLowerASCII(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestStrContains(t *testing.T) {
	cases := []struct {
		s, substr string
		expected  bool
	}{
		{"tap-windows", "tap", true},
		{"tap-windows", "windows", true},
		{"tap-windows", "linux", false},
		{"", "", true},
		{"a", "", true},
		{"", "a", false},
		{"tap0901", "tap0901", true},
		{"TAP-Windows", "tap", false}, // 区分大小写
	}
	for _, c := range cases {
		got := strContains(c.s, c.substr)
		if got != c.expected {
			t.Errorf("strContains(%q, %q) = %v, expected %v", c.s, c.substr, got, c.expected)
		}
	}
}

func TestAdapterNameSet(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters")
	}
	set := adapterGUIDSet()
	for _, a := range adapters {
		if !set[a.GUID] {
			t.Errorf("adapterGUIDSet missing GUID %q (name=%s)", a.GUID, a.Name)
		}
	}
}

func TestFindNewTapAdapter(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters")
	}
	// 用当前全部网卡作为 "before"，应找不到任何新增
	before := adapterGUIDSet()
	newTap := findNewTapAdapter(before)
	if newTap != "" {
		t.Errorf("findNewTapAdapter with full before-set should return \"\", got %q", newTap)
	}

	// 用一个不包含 TAP 的 before 集，应能找到 TAP
	filtered := make(map[string]bool)
	for _, a := range adapters {
		desc := toLowerASCII(a.Desc)
		if strContains(desc, "tap") {
			continue // 排除 TAP
		}
		filtered[a.Name] = true
	}
	newTap = findNewTapAdapter(filtered)
	t.Logf("findNewTapAdapter (excluding TAP from before): %q", newTap)
}

func TestAdapterStorePath(t *testing.T) {
	path, err := adapterStorePath()
	if err != nil {
		t.Fatalf("adapterStorePath failed: %v", err)
	}
	if path == "" {
		t.Fatal("adapterStorePath returned empty path")
	}
	t.Logf("adapter store path: %s", path)
}

func TestSaveLoadAdapterRecord(t *testing.T) {
	adapterStorePathOverride = filepath.Join(t.TempDir(), "adapter.json")
	t.Cleanup(func() { adapterStorePathOverride = "" })

	const testGUID = "{00000000-0000-0000-0000-TEST00000001}"
	const testName = "Test-Adapter"

	saveAdapterRecord(testGUID, testName)
	rec := loadAdapterRecord()
	if rec == nil {
		t.Fatal("loadAdapterRecord returned nil after save")
	}
	if rec.GUID != testGUID {
		t.Errorf("GUID = %q, expected %q", rec.GUID, testGUID)
	}
	if rec.Name != testName {
		t.Errorf("Name = %q, expected %q", rec.Name, testName)
	}
}

func TestLoadAdapterRecordNonexistent(t *testing.T) {
	adapterStorePathOverride = filepath.Join(t.TempDir(), "nonexistent", "adapter.json")
	t.Cleanup(func() { adapterStorePathOverride = "" })

	rec := loadAdapterRecord()
	if rec != nil {
		t.Error("loadAdapterRecord should return nil when file does not exist")
	}
}

func TestFindAdapterByGUID(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters")
	}
	first := adapters[0]
	found := findAdapterByGUID(first.GUID)
	if found == nil {
		t.Fatalf("findAdapterByGUID(%q) returned nil, expected adapter %q", first.GUID, first.Name)
	}
	if found.Name != first.Name {
		t.Errorf("found.Name = %q, expected %q", found.Name, first.Name)
	}

	if a := findAdapterByGUID("{NONEXISTENT-GUID-000000000000}"); a != nil {
		t.Errorf("findAdapterByGUID for nonexistent should return nil, got %v", a)
	}
}

func TestGetAdapterGUID(t *testing.T) {
	adapters, _ := GetAllAdapters()
	if len(adapters) == 0 {
		t.Skip("no adapters")
	}
	first := adapters[0]
	guid := getAdapterGUID(first.Name)
	if guid != first.GUID {
		t.Errorf("getAdapterGUID(%q) = %q, expected %q", first.Name, guid, first.GUID)
	}

	if guid := getAdapterGUID("__nonexistent_adapter__"); guid != "" {
		t.Errorf("getAdapterGUID for nonexistent should return empty, got %q", guid)
	}
}

func TestSaveAdapterRecordEmptyGUID(t *testing.T) {
	adapterStorePathOverride = filepath.Join(t.TempDir(), "adapter.json")
	t.Cleanup(func() { adapterStorePathOverride = "" })

	saveAdapterRecord("", "whatever")
	if _, err := os.Stat(adapterStorePathOverride); err == nil {
		t.Error("saveAdapterRecord with empty GUID should not create a file")
	}
}

func TestLoadAdapterRecordBadJSON(t *testing.T) {
	adapterStorePathOverride = filepath.Join(t.TempDir(), "adapter.json")
	t.Cleanup(func() { adapterStorePathOverride = "" })

	os.WriteFile(adapterStorePathOverride, []byte("not json"), 0600)
	if rec := loadAdapterRecord(); rec != nil {
		t.Error("loadAdapterRecord should return nil for malformed JSON")
	}
}

func TestAdapterStorePathOverride(t *testing.T) {
	tmp := t.TempDir()
	expected := filepath.Join(tmp, "adapter.json")
	adapterStorePathOverride = expected
	t.Cleanup(func() { adapterStorePathOverride = "" })

	path, err := adapterStorePath()
	if err != nil {
		t.Fatalf("adapterStorePath with override failed: %v", err)
	}
	if path != expected {
		t.Errorf("path = %q, expected %q", path, expected)
	}
}
