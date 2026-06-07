package tap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sogame/internal/nic"
)

// RestartAdapter disables then enables an adapter at the device layer.
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
