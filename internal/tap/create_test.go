package tap

import (
	"testing"

	"netjoin/internal/nic"
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
