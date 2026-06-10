package tap

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKnownAdapterStoreSaveLoadDelete(t *testing.T) {
	root := useTempKnownAdapterRoot(t)
	fixedTime := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	oldNow := now
	now = func() time.Time { return fixedTime }
	t.Cleanup(func() { now = oldNow })

	adapter := KnownAdapter{
		NetCfgInstanceID: " {aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee} ",
		LUID:             12345,
		FriendlyName:     " SoGame-VPN ",
		Description:      " TAP-Windows Adapter V9 ",
	}

	if err := SaveKnownAdapter(adapter); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	path := filepath.Join(root, "SoGame", knownAdapterFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected store file at %s: %v", path, err)
	}

	loaded, err := LoadKnownAdapter()
	if err != nil {
		t.Fatalf("LoadKnownAdapter: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadKnownAdapter returned nil")
	}
	if loaded.NetCfgInstanceID != "{aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}" {
		t.Fatalf("NetCfgInstanceID = %q", loaded.NetCfgInstanceID)
	}
	if loaded.LUID != adapter.LUID {
		t.Fatalf("LUID = %d, want %d", loaded.LUID, adapter.LUID)
	}
	if loaded.FriendlyName != "SoGame-VPN" {
		t.Fatalf("FriendlyName = %q", loaded.FriendlyName)
	}
	if loaded.Description != "TAP-Windows Adapter V9" {
		t.Fatalf("Description = %q", loaded.Description)
	}
	if !loaded.UpdatedAt.Equal(fixedTime) {
		t.Fatalf("UpdatedAt = %s, want %s", loaded.UpdatedAt, fixedTime)
	}

	if err := DeleteKnownAdapter(); err != nil {
		t.Fatalf("DeleteKnownAdapter: %v", err)
	}
	loaded, err = LoadKnownAdapter()
	if err != nil {
		t.Fatalf("LoadKnownAdapter after delete: %v", err)
	}
	if loaded != nil {
		t.Fatalf("LoadKnownAdapter after delete = %#v, want nil", loaded)
	}
}

func TestLoadKnownAdapterMissing(t *testing.T) {
	useTempKnownAdapterRoot(t)

	loaded, err := LoadKnownAdapter()
	if err != nil {
		t.Fatalf("LoadKnownAdapter: %v", err)
	}
	if loaded != nil {
		t.Fatalf("LoadKnownAdapter = %#v, want nil", loaded)
	}
}

func useTempKnownAdapterRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldUserConfigDir := userConfigDir
	userConfigDir = func() (string, error) { return root, nil }
	t.Cleanup(func() { userConfigDir = oldUserConfigDir })
	return root
}
