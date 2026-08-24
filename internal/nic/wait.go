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

// WaitAdminStatusByNetCfgID 等待指定 NetCfgInstanceId 的网卡进入目标管理状态。
func WaitAdminStatusByNetCfgID(ctx context.Context, netCfgID string, want uint32, interval, timeout time.Duration) error {
	label := fmt.Sprintf("adapter netcfg=%s admin status %s", netCfgID, Info{AdminStatus: want}.AdminText())
	return poll.WaitUntil(ctx, interval, timeout, func() (bool, error) {
		info, err := FindByNetCfgID(netCfgID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return info.AdminStatus == want, nil
	}, label)
}
