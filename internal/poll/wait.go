package poll

import (
	"context"
	"fmt"
	"time"
)

// WaitUntil 轮询 fn，直到条件满足、返回错误或等待超时。
func WaitUntil(ctx context.Context, interval, timeout time.Duration, fn func() (bool, error), label string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = interval
	}
	if label == "" {
		label = "condition"
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		ok, err := fn()
		if err != nil {
			return fmt.Errorf("wait %s: %w", label, err)
		}
		if ok {
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait %s: %w", label, ctx.Err())
		case <-timer.C:
		}
	}
}
