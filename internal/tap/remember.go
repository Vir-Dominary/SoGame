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

	"sogame/internal/nic"
)

func RememberKnownAdapter(info nic.Info, netCfgID string) error {
	id := strings.TrimSpace(netCfgID)
	if id == "" {
		resolvedID, err := netCfgIDFromLuid(info.Luid)
		if err != nil {
			return fmt.Errorf("resolve TAP NetCfgInstanceId: %w", err)
		}
		id = resolvedID
	}

	return SaveKnownAdapter(KnownAdapter{
		NetCfgInstanceID: id,
		LUID:             info.Luid,
		FriendlyName:     info.FriendlyName,
		Description:      info.Description,
	})
}

func RememberKnownAdapterByFriendlyName(name string) (*nic.Info, error) {
	info, err := findByFriendlyName(name)
	if err != nil {
		return nil, err
	}
	if err := RememberKnownAdapter(*info, ""); err != nil {
		return nil, err
	}
	return info, nil
}
