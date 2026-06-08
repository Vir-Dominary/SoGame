package tap

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sogame/internal/nic"
)

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

	if got := fmt.Sprint(stub.statusCalls); got != "[{11 true}]" {
		t.Fatalf("status calls = %s", got)
	}
	if got := fmt.Sprint(stub.waitCalls); got != "[{11 1}]" {
		t.Fatalf("wait calls = %s", got)
	}
}

func TestRestartAdapterTogglesDevice(t *testing.T) {
	stub := stubDeviceOps(t)

	if err := RestartAdapter(nic.Info{Luid: 11, AdminStatus: nic.AdminUp}); err != nil {
		t.Fatalf("RestartAdapter: %v", err)
	}

	if got := fmt.Sprint(stub.statusCalls); got != "[{11 false} {11 true}]" {
		t.Fatalf("status calls = %s", got)
	}
	if got := fmt.Sprint(stub.waitCalls); got != "[{11 2} {11 1}]" {
		t.Fatalf("wait calls = %s", got)
	}
}

func TestEnableAdapterByName(t *testing.T) {
	stub := stubDeviceOps(t)
	stub.byName[testAdapterName] = nic.Info{FriendlyName: testAdapterName, Luid: 11, AdminStatus: nic.AdminDown}

	if err := EnableAdapterByName(testAdapterName); err != nil {
		t.Fatalf("EnableAdapterByName: %v", err)
	}
	if got := fmt.Sprint(stub.statusCalls); got != "[{11 true}]" {
		t.Fatalf("status calls = %s", got)
	}
}

type deviceOpsStub struct {
	byName      map[string]nic.Info
	statusCalls []struct {
		luid   uint64
		enable bool
	}
	waitCalls []struct {
		luid uint64
		want uint32
	}
}

func stubDeviceOps(t *testing.T) *deviceOpsStub {
	t.Helper()
	stub := &deviceOpsStub{byName: make(map[string]nic.Info)}
	oldFindByFriendlyName := findByFriendlyName
	oldSetDeviceStatus := setDeviceStatus
	oldWaitAdminStatus := waitAdminStatus

	findByFriendlyName = func(name string) (*nic.Info, error) {
		info, ok := stub.byName[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", nic.ErrNotFound, name)
		}
		return &info, nil
	}
	setDeviceStatus = func(luid uint64, enable bool) error {
		stub.statusCalls = append(stub.statusCalls, struct {
			luid   uint64
			enable bool
		}{luid: luid, enable: enable})
		return nil
	}
	waitAdminStatus = func(_ context.Context, luid uint64, want uint32, _, _ time.Duration) error {
		stub.waitCalls = append(stub.waitCalls, struct {
			luid uint64
			want uint32
		}{luid: luid, want: want})
		return nil
	}

	t.Cleanup(func() {
		findByFriendlyName = oldFindByFriendlyName
		setDeviceStatus = oldSetDeviceStatus
		waitAdminStatus = oldWaitAdminStatus
	})
	return stub
}
