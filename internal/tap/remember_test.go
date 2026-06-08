package tap

import (
	"fmt"
	"testing"

	"netjoin/internal/nic"
)

func TestRememberKnownAdapterResolvesNetCfgID(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubRemember(t, map[string]nic.Info{}, map[uint64]string{11: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"})

	if err := RememberKnownAdapter(tapInfo(11, testAdapterName), ""); err != nil {
		t.Fatalf("RememberKnownAdapter: %v", err)
	}

	known, err := LoadKnownAdapter()
	if err != nil {
		t.Fatalf("LoadKnownAdapter: %v", err)
	}
	if known == nil {
		t.Fatal("LoadKnownAdapter returned nil")
	}
	if known.NetCfgInstanceID != "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}" {
		t.Fatalf("NetCfgInstanceID = %q", known.NetCfgInstanceID)
	}
	if known.LUID != 11 || known.FriendlyName != testAdapterName {
		t.Fatalf("known = %#v", known)
	}
}

func TestRememberKnownAdapterByFriendlyName(t *testing.T) {
	useTempKnownAdapterRoot(t)
	stubRemember(t,
		map[string]nic.Info{testAdapterName: tapInfo(11, testAdapterName)},
		map[uint64]string{11: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"},
	)

	info, err := RememberKnownAdapterByFriendlyName(testAdapterName)
	if err != nil {
		t.Fatalf("RememberKnownAdapterByFriendlyName: %v", err)
	}
	if info == nil || info.Luid != 11 {
		t.Fatalf("info = %#v", info)
	}
}

func stubRemember(t *testing.T, byName map[string]nic.Info, netCfgByLuid map[uint64]string) {
	t.Helper()
	oldFindByFriendlyName := findByFriendlyName
	oldNetCfgIDFromLuid := netCfgIDFromLuid

	findByFriendlyName = func(name string) (*nic.Info, error) {
		info, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", nic.ErrNotFound, name)
		}
		return &info, nil
	}
	netCfgIDFromLuid = func(luid uint64) (string, error) {
		id, ok := netCfgByLuid[luid]
		if !ok {
			return "", fmt.Errorf("%w: luid=%d", nic.ErrNotFound, luid)
		}
		return id, nil
	}

	t.Cleanup(func() {
		findByFriendlyName = oldFindByFriendlyName
		netCfgIDFromLuid = oldNetCfgIDFromLuid
	})
}
