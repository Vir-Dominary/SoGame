package tap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sogame/internal/nic"
)

// Internal function variables for test stubbing.
var (
	setDeviceStatusByNetCfgID   = nic.SetDeviceStatusByNetCfgID
	waitAdminStatusByNetCfgID   = nic.WaitAdminStatusByNetCfgID
)

// EnableAdapter enables an adapter by its NIC info using device-layer APIs.
func EnableAdapter(info nic.Info) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if info.AdminStatus == nic.AdminUp {
		return nil
	}

	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		return fmt.Errorf("resolve NetCfgInstanceID for adapter LUID=%d: %w", info.Luid, err)
	}

	if err := setDeviceStatusByNetCfgID(netCfgID, true); err != nil {
		return fmt.Errorf("enable adapter %q: %w", info.FriendlyName, err)
	}
	if err := waitAdminStatusByNetCfgID(ctx, netCfgID, nic.AdminUp, 200*time.Millisecond, 10*time.Second); err != nil {
		return fmt.Errorf("wait adapter %q admin up: %w", info.FriendlyName, err)
	}
	return nil
}

// EnableAdapterByName enables an adapter by its friendly name.
func EnableAdapterByName(name string) error {
	target := strings.TrimSpace(name)
	if target == "" {
		return fmt.Errorf("adapter name is empty")
	}

	info, err := findByFriendlyName(target)
	if err != nil {
		return fmt.Errorf("find adapter %q: %w", target, err)
	}
	return EnableAdapter(*info)
}

// RestartAdapter disables then enables an adapter by its NIC info at the device layer.
func RestartAdapterInfo(info nic.Info) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		return fmt.Errorf("resolve NetCfgInstanceID for adapter LUID=%d: %w", info.Luid, err)
	}

	if err := setDeviceStatusByNetCfgID(netCfgID, false); err != nil {
		return fmt.Errorf("disable adapter %q: %w", info.FriendlyName, err)
	}
	if err := waitAdminStatusByNetCfgID(ctx, netCfgID, nic.AdminDown, 200*time.Millisecond, 10*time.Second); err != nil {
		return fmt.Errorf("wait adapter %q admin down: %w", info.FriendlyName, err)
	}
	if err := setDeviceStatusByNetCfgID(netCfgID, true); err != nil {
		return fmt.Errorf("enable adapter %q: %w", info.FriendlyName, err)
	}
	if err := waitAdminStatusByNetCfgID(ctx, netCfgID, nic.AdminUp, 200*time.Millisecond, 10*time.Second); err != nil {
		return fmt.Errorf("wait adapter %q admin up: %w", info.FriendlyName, err)
	}
	return nil
}

// RestartAdapter disables then enables an adapter at the device layer by name.
func RestartAdapter(ctx context.Context, name string, timeout time.Duration) error {
	target := strings.TrimSpace(name)
	if target == "" {
		return fmt.Errorf("adapter name is empty")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	info, err := nic.FindByFriendlyName(target)
	if err != nil {
		return fmt.Errorf("find adapter %q: %w", target, err)
	}
	if err := nic.SetDeviceStatus(info.Luid, false); err != nil {
		return fmt.Errorf("disable adapter %q: %w", target, err)
	}
	if err := nic.WaitAdminStatus(ctx, info.Luid, nic.AdminDown, 200*time.Millisecond, timeout); err != nil {
		return fmt.Errorf("wait adapter %q admin down: %w", target, err)
	}
	if err := enableAdapter(ctx, target, info.Luid, timeout); err != nil {
		return err
	}
	return nil
}

func enableAdapter(ctx context.Context, name string, luid uint64, timeout time.Duration) error {
	if err := nic.SetDeviceStatus(luid, true); err != nil {
		return fmt.Errorf("enable adapter %q: %w", name, err)
	}
	if err := nic.WaitAdminStatus(ctx, luid, nic.AdminUp, 200*time.Millisecond, timeout); err != nil {
		return fmt.Errorf("wait adapter %q admin up: %w", name, err)
	}
	return nil
}
