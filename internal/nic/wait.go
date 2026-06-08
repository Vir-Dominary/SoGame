//go:build windows

package nic

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sogame/internal/poll"
)

// WaitAdminStatus 等待网卡进入指定的管理状态。
func WaitAdminStatus(ctx context.Context, luid uint64, want uint32, interval, timeout time.Duration) error {
	label := fmt.Sprintf("adapter luid=%d admin status %s", luid, Info{AdminStatus: want}.AdminText())
	return poll.WaitUntil(ctx, interval, timeout, func() (bool, error) {
		info, err := FindByLuid(luid)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return info.AdminStatus == want, nil
	}, label)
}
