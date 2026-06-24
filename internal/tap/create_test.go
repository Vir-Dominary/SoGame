package tap

import (
	"testing"

	"sogame/internal/nic"
)

func TestFindNewWindowsAdapter(t *testing.T) {
	before := []nic.Info{
		tapInfo(1, "Old TAP"),
		{Luid: 2, FriendlyName: "Ethernet", Description: "Ethernet Adapter"},
	}
	after := []nic.Info{
		tapInfo(1, "Old TAP"),
		{Luid: 2, FriendlyName: "Ethernet", Description: "Ethernet Adapter"},
		tapInfo(3, "New TAP"),
	}

	found, err := FindNewWindowsAdapter(before, after)
	if err != nil {
		t.Fatalf("FindNewWindowsAdapter: %v", err)
	}
	if found.Luid != 3 {
		t.Fatalf("LUID = %d, want 3", found.Luid)
	}
}

func TestFindNewWindowsAdapterNone(t *testing.T) {
	_, err := FindNewWindowsAdapter([]nic.Info{tapInfo(1, "Old TAP")}, []nic.Info{tapInfo(1, "Old TAP")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFindNewWindowsAdapterMultiple(t *testing.T) {
	_, err := FindNewWindowsAdapter(nil, []nic.Info{tapInfo(1, "New TAP 1"), tapInfo(2, "New TAP 2")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFindNewWindowsAdapterIgnoresNonTap(t *testing.T) {
	_, err := FindNewWindowsAdapter(nil, []nic.Info{{Luid: 1, FriendlyName: "Ethernet", Description: "Ethernet Adapter"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestFindNewWindowsAdapterIgnoresFilterMiniport 验证 IsFilterInterface=true 的
// 过滤 miniport 不会被选为新建 TAP 候选。
// 注意：实际场景中 ListWindowsAdapters 已过滤 miniport，这里直接验证
// FindNewWindowsAdapter 对带 IsFilterInterface 标志的候选的处理。
func TestFindNewWindowsAdapterIgnoresFilterMiniport(t *testing.T) {
	before := []nic.Info{tapInfo(1, "Old TAP")}
	after := []nic.Info{
		tapInfo(1, "Old TAP"),
		tapInfo(3, "New TAP"),
		// cFosSpeed miniport：继承 TAP 描述但 IsFilterInterface=true
		{Luid: 4, FriendlyName: "New TAP-cFosSpeed-0000", Description: "TAP-Windows Adapter V9", IsFilterInterface: true},
	}

	// 由于 miniport 的 IsFilterInterface=true，但它仍会被 IsWindowsDescription 匹配。
	// FindNewWindowsAdapter 本身不过滤 IsFilterInterface（依赖 ListWindowsAdapters 过滤），
	// 所以这里会报"不唯一"。此测试验证：如果 miniport 漏过 ListWindowsAdapters 过滤，
	// FindNewWindowsAdapter 会报错而非静默选错。
	_, err := FindNewWindowsAdapter(before, after)
	if err == nil {
		t.Fatal("expected error when filter miniport leaks into FindNewWindowsAdapter")
	}
}
