package tap

import (
	"fmt"
	"testing"

	"netjoin/internal/nic"
)

const testAdapterName = "SoGame-VPN"

func TestResolveKnownAdapterNoKnown(t *testing.T) {
	useTempKnownAdapterRoot(t)

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveNoKnownAdapter {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveNoKnownAdapter)
	}
}

func TestResolveKnownAdapterFoundByNetCfgID(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubTapResolve(t,
		map[string]nic.Info{
			"{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}": tapInfo(11, testAdapterName),
		},
		nil,
	)

	if err := SaveKnownAdapter(KnownAdapter{NetCfgInstanceID: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"}); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveFound {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveFound)
	}
	if result.Info == nil || result.Info.Luid != 11 {
		t.Fatalf("Info = %#v", result.Info)
	}
}

func TestResolveKnownAdapterMissingByNetCfgID(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubTapResolve(t, nil, nil)

	if err := SaveKnownAdapter(KnownAdapter{NetCfgInstanceID: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"}); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveMissing {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveMissing)
	}
}

func TestResolveKnownAdapterInvalidDescription(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubTapResolve(t,
		map[string]nic.Info{
			"{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}": {
				FriendlyName: testAdapterName,
				Description:  "Ethernet Adapter",
				Luid:         11,
			},
		},
		nil,
	)

	if err := SaveKnownAdapter(KnownAdapter{NetCfgInstanceID: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"}); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveInvalid {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveInvalid)
	}
}

func TestResolveKnownAdapterNameMismatch(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubTapResolve(t,
		map[string]nic.Info{
			"{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}": tapInfo(11, "OpenVPN TAP"),
		},
		nil,
	)

	if err := SaveKnownAdapter(KnownAdapter{NetCfgInstanceID: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"}); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveNameMismatch {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveNameMismatch)
	}
}

func TestResolveKnownAdapterLUIDMigration(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubTapResolve(t,
		nil,
		map[uint64]nic.Info{11: tapInfo(11, testAdapterName)},
	)

	if err := SaveKnownAdapter(KnownAdapter{LUID: 11}); err != nil {
		t.Fatalf("SaveKnownAdapter: %v", err)
	}

	result, err := ResolveKnownAdapter(testAdapterName)
	if err != nil {
		t.Fatalf("ResolveKnownAdapter: %v", err)
	}
	if result.Status != ResolveFound {
		t.Fatalf("Status = %s, want %s", result.Status, ResolveFound)
	}
	if result.NetCfgInstanceID != "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}" {
		t.Fatalf("NetCfgInstanceID = %q", result.NetCfgInstanceID)
	}
}

func stubTapResolve(t *testing.T, byNetCfg map[string]nic.Info, byLuid map[uint64]nic.Info) {
	t.Helper()
	oldFindByNetCfgID := findByNetCfgID
	oldFindByLuid := findByLuid
	oldNetCfgIDFromLuid := netCfgIDFromLuid

	findByNetCfgID = func(netCfgID string) (*nic.Info, error) {
		info, ok := byNetCfg[netCfgID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", nic.ErrNotFound, netCfgID)
		}
		return &info, nil
	}
	findByLuid = func(luid uint64) (*nic.Info, error) {
		info, ok := byLuid[luid]
		if !ok {
			return nil, fmt.Errorf("%w: luid=%d", nic.ErrNotFound, luid)
		}
		return &info, nil
	}
	netCfgIDFromLuid = func(luid uint64) (string, error) {
		if _, ok := byLuid[luid]; !ok {
			return "", fmt.Errorf("%w: luid=%d", nic.ErrNotFound, luid)
		}
		return "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}", nil
	}

	t.Cleanup(func() {
		findByNetCfgID = oldFindByNetCfgID
		findByLuid = oldFindByLuid
		netCfgIDFromLuid = oldNetCfgIDFromLuid
	})
}

func tapInfo(luid uint64, name string) nic.Info {
	return nic.Info{
		FriendlyName: name,
		Description:  "TAP-Windows Adapter V9",
		Luid:         luid,
	}
}
