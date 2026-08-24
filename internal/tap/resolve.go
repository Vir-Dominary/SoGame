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
	"errors"
	"strings"

	"sogame/internal/nic"
)

type ResolveStatus string

const (
	ResolveNoKnownAdapter ResolveStatus = "NoKnownAdapter"
	ResolveFound          ResolveStatus = "Found"
	ResolveMissing        ResolveStatus = "Missing"
	ResolveInvalid        ResolveStatus = "Invalid"
	ResolveNameMismatch   ResolveStatus = "NameMismatch"
)

var (
	findByNetCfgID     = nic.FindByNetCfgID
	findByFriendlyName = nic.FindByFriendlyName
	findByLuid         = nic.FindByLuid
	netCfgIDFromLuid   = nic.NetCfgIDFromLuid
)

type ResolveResult struct {
	Status           ResolveStatus
	Known            *KnownAdapter
	Info             *nic.Info
	NetCfgInstanceID string
}

func ResolveKnownAdapter(expectedName string) (ResolveResult, error) {
	known, err := LoadKnownAdapter()
	if err != nil {
		return ResolveResult{}, err
	}
	if known == nil {
		return ResolveResult{Status: ResolveNoKnownAdapter}, nil
	}

	if netCfgID := strings.TrimSpace(known.NetCfgInstanceID); netCfgID != "" {
		info, err := findByNetCfgID(netCfgID)
		if err != nil {
			if errors.Is(err, nic.ErrNotFound) {
				return ResolveResult{Status: ResolveMissing, Known: known, NetCfgInstanceID: netCfgID}, nil
			}
			return ResolveResult{}, err
		}
		return classifyResolvedAdapter(known, info, netCfgID, expectedName), nil
	}

	if known.LUID == 0 {
		return ResolveResult{Status: ResolveNoKnownAdapter, Known: known}, nil
	}

	info, err := findByLuid(known.LUID)
	if err != nil {
		if errors.Is(err, nic.ErrNotFound) {
			return ResolveResult{Status: ResolveMissing, Known: known}, nil
		}
		return ResolveResult{}, err
	}
	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		if errors.Is(err, nic.ErrNotFound) {
			return ResolveResult{Status: ResolveInvalid, Known: known, Info: info}, nil
		}
		return ResolveResult{}, err
	}
	return classifyResolvedAdapter(known, info, netCfgID, expectedName), nil
}

func classifyResolvedAdapter(known *KnownAdapter, info *nic.Info, netCfgID, expectedName string) ResolveResult {
	result := ResolveResult{
		Status:           ResolveFound,
		Known:            known,
		Info:             info,
		NetCfgInstanceID: strings.TrimSpace(netCfgID),
	}
	if info == nil || !IsWindowsDescription(info.Description) {
		result.Status = ResolveInvalid
		return result
	}
	if expectedName != "" && !strings.EqualFold(info.FriendlyName, expectedName) {
		result.Status = ResolveNameMismatch
	}
	return result
}
