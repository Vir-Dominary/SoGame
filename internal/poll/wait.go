// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

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
