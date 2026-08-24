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
	"testing"

	"sogame/internal/nic"
)

func TestFindNewWindowsAdapter(t *testing.T) {
	before := []nic.Info{
		tapInfo(1, "Old TAP"),
		{Luid: 2, FriendlyName: "Ethernet", Description: "Ethernet Adapter"},
	}
	after := []nic.Info{
		tapInfo(1, "Old TAP"),
		{Luid: 2, FriendlyName: "Ethernet", Description: "Ethernet Adapter"},
		tapInfo(3, "New TAP"),
	}

	found, err := FindNewWindowsAdapter(before, after)
	if err != nil {
		t.Fatalf("FindNewWindowsAdapter: %v", err)
	}
	if found.Luid != 3 {
		t.Fatalf("LUID = %d, want 3", found.Luid)
	}
}

func TestFindNewWindowsAdapterNone(t *testing.T) {
	_, err := FindNewWindowsAdapter([]nic.Info{tapInfo(1, "Old TAP")}, []nic.Info{tapInfo(1, "Old TAP")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFindNewWindowsAdapterMultiple(t *testing.T) {
	_, err := FindNewWindowsAdapter(nil, []nic.Info{tapInfo(1, "New TAP 1"), tapInfo(2, "New TAP 2")})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFindNewWindowsAdapterIgnoresNonTap(t *testing.T) {
	_, err := FindNewWindowsAdapter(nil, []nic.Info{{Luid: 1, FriendlyName: "Ethernet", Description: "Ethernet Adapter"}})
	if err == nil {
		t.Fatal("expected error")
	}
}
