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

package tap

import (
	"fmt"
	"strings"
	"time"

	"sogame/internal/nic"
)

// IsLikeDescription reports whether an interface description looks like a TAP/TUN adapter.
func IsLikeDescription(description string) bool {
	desc := strings.ToLower(description)
	return strings.Contains(desc, "tap-windows") ||
		strings.Contains(desc, "tap0901") ||
		strings.Contains(desc, "tap") ||
		strings.Contains(desc, "wintun") ||
		strings.Contains(desc, "tun")
}

// IsWindowsDescription reports whether an interface description is a TAP-Windows adapter.
func IsWindowsDescription(description string) bool {
	desc := strings.ToLower(description)
	return strings.Contains(desc, "tap-windows") ||
		strings.Contains(desc, "tap0901")
}

// FindAdapter returns the named adapter first, then any TAP-Windows adapter.
// Note: fallback uses IsWindowsDescription (not IsLikeDescription) to avoid
// matching WinTUN or other non-TAP-Windows adapters that n2n edge cannot use.
func FindAdapter(name string) (*nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(name)
	if target != "" {
		for i := range list {
			if strings.EqualFold(list[i].FriendlyName, target) {
				return &list[i], nil
			}
		}
	}
	for i := range list {
		if IsWindowsDescription(list[i].Description) {
			return &list[i], nil
		}
	}

	return nil, fmt.Errorf("%w: TAP adapter", nic.ErrNotFound)
}

// HasWindowsAdapter reports whether any TAP-Windows adapter instance exists.
func HasWindowsAdapter() (bool, error) {
	list, err := nic.List()
	if err != nil {
		return false, err
	}
	for i := range list {
		if IsWindowsDescription(list[i].Description) {
			return true, nil
		}
	}
	return false, nil
}

// FindRenameCandidate returns a TAP-Windows adapter that is not already named newName.
func FindRenameCandidate(newName string) (*nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(newName)
	for i := range list {
		if target != "" && strings.EqualFold(list[i].FriendlyName, target) {
			continue
		}
		if IsWindowsDescription(list[i].Description) {
			return &list[i], nil
		}
	}

	return nil, fmt.Errorf("%w: renameable TAP adapter", nic.ErrNotFound)
}

// RenameCandidate renames the first TAP-Windows candidate and verifies the new name.
// Returns the adapter info with FriendlyName updated to the new name.
func RenameCandidate(newName string, timeout time.Duration) (*nic.Info, error) {
	target := strings.TrimSpace(newName)
	if target == "" {
		return nil, fmt.Errorf("adapter name is empty")
	}

	info, err := FindRenameCandidate(target)
	if err != nil {
		return nil, err
	}
	if err := nic.RenameConnection(info.Luid, target); err != nil {
		return nil, fmt.Errorf("rename TAP adapter %q to %q: %w", info.FriendlyName, target, err)
	}
	if err := waitFriendlyName(target, timeout); err != nil {
		return nil, fmt.Errorf("verify renamed TAP adapter %q: %w", target, err)
	}
	// 返回更新后的 Info，FriendlyName 为新名称
	info.FriendlyName = target
	return info, nil
}

func waitFriendlyName(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if _, err := nic.FindByFriendlyName(name); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(200 * time.Millisecond)
	}
}
