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

func TestNormalizeNetCfgID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty", id: "", want: ""},
		{name: "trim", id: "  {aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee}  ", want: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"},
		{name: "missing braces", id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", want: "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNetCfgID(tt.id); got != tt.want {
				t.Fatalf("normalizeNetCfgID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestNetCfgFromLuid(t *testing.T) {
	info := sampleWithNetCfgID(t)

	id, err := NetCfgIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgIDFromLuid(%d): %v", info.Luid, err)
	}
	if id == "" {
		t.Fatal("NetCfgIDFromLuid returned empty id")
	}
	if !strings.HasPrefix(id, "{") || !strings.HasSuffix(id, "}") {
		t.Fatalf("NetCfgIDFromLuid returned non-canonical id: %q", id)
	}

	guid, err := NetCfgGUIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgGUIDFromLuid(%d): %v", info.Luid, err)
	}
	guidStr := strings.ToUpper(guid.String())
	if guidStr == "" {
		t.Fatal("NetCfgGUIDFromLuid returned empty GUID")
	}
	if guidStr != id {
		t.Fatalf("NetCfgGUIDFromLuid mismatch: guid=%q, id=%q", guidStr, id)
	}
}

func TestNetCfgFromLuid_NotFound(t *testing.T) {
	_, err := NetCfgIDFromLuid(^uint64(0))
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	_, err = NetCfgGUIDFromLuid(^uint64(0))
	if err == nil {
		t.Fatal("expected error for invalid Luid")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLuidFromNetCfgID(t *testing.T) {
	info := sampleWithNetCfgID(t)
	id, err := NetCfgIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgIDFromLuid(%d): %v", info.Luid, err)
	}

	luid, err := LuidFromNetCfgID(id)
	if err != nil {
		t.Fatalf("LuidFromNetCfgID(%q): %v", id, err)
	}
	if luid != info.Luid {
		t.Fatalf("LuidFromNetCfgID(%q) = %d, want %d", id, luid, info.Luid)
	}

	withoutBraces := strings.Trim(id, "{}")
	luid, err = LuidFromNetCfgID(strings.ToLower(withoutBraces))
	if err != nil {
		t.Fatalf("LuidFromNetCfgID(lowercase without braces): %v", err)
	}
	if luid != info.Luid {
		t.Fatalf("LuidFromNetCfgID(lowercase without braces) = %d, want %d", luid, info.Luid)
	}
}

func TestFindByNetCfgID(t *testing.T) {
	info := sampleWithNetCfgID(t)
	id, err := NetCfgIDFromLuid(info.Luid)
	if err != nil {
		t.Fatalf("NetCfgIDFromLuid(%d): %v", info.Luid, err)
	}

	found, err := FindByNetCfgID(id)
	if err != nil {
		t.Fatalf("FindByNetCfgID(%q): %v", id, err)
	}
	if found.Luid != info.Luid {
		t.Fatalf("FindByNetCfgID(%q) LUID = %d, want %d", id, found.Luid, info.Luid)
	}
}

func TestLuidFromNetCfgID_NotFound(t *testing.T) {
	_, err := LuidFromNetCfgID("{00000000-0000-0000-0000-000000000000}")
	if err == nil {
		t.Fatal("expected error for non-existent NetCfgInstanceId")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	_, err = FindByNetCfgID("")
	if err == nil {
		t.Fatal("expected error for empty NetCfgInstanceId")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func sampleWithNetCfgID(t *testing.T) Info {
	t.Helper()

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, info := range list {
		if _, err := NetCfgIDFromLuid(info.Luid); err == nil {
			return info
		}
	}
	t.Skip("no adapter with NetCfgInstanceId found")
	return Info{}
}
