package tap

import (
	"context"
	"fmt"
	"testing"
	"time"

	"netjoin/internal/nic"
)

const testNetCfgID = "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"

func TestEnableAdapterAlreadyUp(t *testing.T) {
	stubDeviceOps(t)

	if err := EnableAdapter(nic.Info{Luid: 11, AdminStatus: nic.AdminUp}); err != nil {
		t.Fatalf("EnableAdapter: %v", err)
	}
}

func TestEnableAdapterSetsDeviceUp(t *testing.T) {
	stub := stubDeviceOps(t)

	if err := EnableAdapter(nic.Info{Luid: 11, AdminStatus: nic.AdminDown}); err != nil {
		t.Fatalf("EnableAdapter: %v", err)
	}

	if got := fmt.Sprint(stub.statusCalls); got != "[{"+testNetCfgID+" true}]" {
		t.Fatalf("status calls = %s", got)
	}
	if got := fmt.Sprint(stub.waitCalls); got != "[{"+testNetCfgID+" 1}]" {
		t.Fatalf("wait calls = %s", got)
	}
}

func TestRestartAdapterTogglesDevice(t *testing.T) {
	stub := stubDeviceOps(t)

	if err := RestartAdapter(nic.Info{Luid: 11, AdminStatus: nic.AdminUp}); err != nil {
		t.Fatalf("RestartAdapter: %v", err)
	}

	if got := fmt.Sprint(stub.statusCalls); got != "[{"+testNetCfgID+" false} {"+testNetCfgID+" true}]" {
		t.Fatalf("status calls = %s", got)
	}
	if got := fmt.Sprint(stub.waitCalls); got != "[{"+testNetCfgID+" 2} {"+testNetCfgID+" 1}]" {
		t.Fatalf("wait calls = %s", got)
	}
}

func TestEnableAdapterByName(t *testing.T) {
	stub := stubDeviceOps(t)
	stub.byName[testAdapterName] = nic.Info{FriendlyName: testAdapterName, Luid: 11, AdminStatus: nic.AdminDown}

	if err := EnableAdapterByName(testAdapterName); err != nil {
		t.Fatalf("EnableAdapterByName: %v", err)
	}
	if got := fmt.Sprint(stub.statusCalls); got != "[{"+testNetCfgID+" true}]" {
		t.Fatalf("status calls = %s", got)
	}
}

type deviceOpsStub struct {
	byName      map[string]nic.Info
	statusCalls []struct {
		netCfgID string
		enable   bool
	}
	waitCalls []struct {
		netCfgID string
		want     uint32
	}
}

func stubDeviceOps(t *testing.T) *deviceOpsStub {
	t.Helper()
	stub := &deviceOpsStub{byName: make(map[string]nic.Info)}
	oldFindByFriendlyName := findByFriendlyName
	oldNetCfgIDFromLuid := netCfgIDFromLuid
	oldSetDeviceStatusByNetCfgID := setDeviceStatusByNetCfgID
	oldWaitAdminStatusByNetCfgID := waitAdminStatusByNetCfgID

	findByFriendlyName = func(name string) (*nic.Info, error) {
		info, ok := stub.byName[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", nic.ErrNotFound, name)
		}
		return &info, nil
	}
	netCfgIDFromLuid = func(luid uint64) (string, error) {
		if luid != 11 {
			return "", fmt.Errorf("%w: luid=%d", nic.ErrNotFound, luid)
		}
		return testNetCfgID, nil
	}
	setDeviceStatusByNetCfgID = func(netCfgID string, enable bool) error {
		stub.statusCalls = append(stub.statusCalls, struct {
			netCfgID string
			enable   bool
		}{netCfgID: netCfgID, enable: enable})
		return nil
	}
	waitAdminStatusByNetCfgID = func(_ context.Context, netCfgID string, want uint32, _, _ time.Duration) error {
		stub.waitCalls = append(stub.waitCalls, struct {
			netCfgID string
			want     uint32
		}{netCfgID: netCfgID, want: want})
		return nil
	}

	t.Cleanup(func() {
		findByFriendlyName = oldFindByFriendlyName
		netCfgIDFromLuid = oldNetCfgIDFromLuid
		setDeviceStatusByNetCfgID = oldSetDeviceStatusByNetCfgID
		waitAdminStatusByNetCfgID = oldWaitAdminStatusByNetCfgID
	})
	return stub
}
