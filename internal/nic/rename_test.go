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
	"strings"
	"testing"
)

func TestRenameConnectionEmptyName(t *testing.T) {
        if err := RenameConnection(1, " "); err == nil {
                t.Fatal("expected error for empty connection name")
        }
}

func TestRenameConnectionInvalidName(t *testing.T) {
        if err := RenameConnection(1, "bad\x00name"); err == nil {
                t.Fatal("expected error for invalid connection name")
        }
}

func TestRenameConnectionByNetCfgIDEmptyName(t *testing.T) {
        err := RenameConnectionByNetCfgID("{00000000-0000-0000-0000-000000000000}", " ")
        if err == nil || !strings.Contains(err.Error(), "adapter name is empty") {
                t.Fatalf("expected empty name error, got %v", err)
        }
}

func TestRenameConnectionInvalidLuid(t *testing.T) {
        err := RenameConnection(^uint64(0), "SoGame-Invalid-Rename")

	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestHrRenameConnectionAvailable(t *testing.T) {
	if err := procHrRenameConnection.Find(); err != nil {
		t.Fatalf("HrRenameConnection should be available: %v", err)
	}
}

func TestRenameConnectionByNetCfgIDInvalid(t *testing.T) {
        err := RenameConnectionByNetCfgID(
                "{00000000-0000-0000-0000-000000000000}",
                "SoGame-Invalid-Rename",
        )
        if err == nil {
                t.Fatal("expected error for invalid NetCfgInstanceId")
        }
}
