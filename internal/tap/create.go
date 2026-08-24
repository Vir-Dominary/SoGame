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

	"sogame/internal/nic"
)

func ListWindowsAdapters() ([]nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	var taps []nic.Info
	for _, info := range list {
		if IsWindowsDescription(info.Description) {
			taps = append(taps, info)
		}
	}
	return taps, nil
}

func FindNewWindowsAdapter(before, after []nic.Info) (*nic.Info, error) {
	seen := make(map[uint64]bool, len(before))
	for _, info := range before {
		seen[info.Luid] = true
	}

	var candidates []nic.Info
	for _, info := range after {
		if seen[info.Luid] || !IsWindowsDescription(info.Description) {
			continue
		}
		candidates = append(candidates, info)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("未找到新建的 TAP-Windows 适配器")
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("新建 TAP-Windows 适配器不唯一: %d", len(candidates))
	}
	return &candidates[0], nil
}

func RenameAdapter(luid uint64, newName string) error {
	return nic.RenameConnection(luid, newName)
}
