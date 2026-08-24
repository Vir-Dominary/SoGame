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
	"errors"
	"testing"
)

func TestSetDeviceStatusInvalidLuid(t *testing.T) {
	err := SetDeviceStatus(^uint64(0), true)
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetDeviceStatusByNameNotFound(t *testing.T) {
	err := SetDeviceStatusByName("SoGame-NonExistent-Adapter-00000000", true)
	if err == nil {
		t.Fatal("expected error for non-existent adapter")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSetDeviceStatusByNetCfgIDNotFound(t *testing.T) {
	err := SetDeviceStatusByNetCfgID("{00000000-0000-0000-0000-000000000000}", true)
	if err == nil {
		t.Fatal("expected error for non-existent NetCfgInstanceId")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindDeviceByNetCfgIDNotFound(t *testing.T) {
	devInfo, _, err := findDeviceByNetCfgID("{00000000-0000-0000-0000-000000000000}")
	if devInfo != 0 {
		devInfo.Close()
	}
	if err == nil {
		t.Fatal("expected error for non-existent NetCfgInstanceId")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
