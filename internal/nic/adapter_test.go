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

package nic

import (
	"errors"
	"testing"
)

func TestAdminText(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{1, "启用"},
		{2, "禁用"},
		{3, "测试"},
		{99, "其它(99)"},
	}
	for _, tt := range tests {
		got := Info{AdminStatus: tt.status}.AdminText()
		if got != tt.want {
			t.Errorf("AdminStatus=%d: got %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestOperText(t *testing.T) {
	tests := []struct {
		status uint32
		want   string
	}{
		{1, "已连接"},
		{2, "已断开"},
		{4, "未知"},
		{6, "不存在"},
		{99, "其它(99)"},
	}
	for _, tt := range tests {
		got := Info{OperStatus: tt.status}.OperText()
		if got != tt.want {
			t.Errorf("OperStatus=%d: got %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestErrNotFound(t *testing.T) {
	err := ErrNotFound
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("ErrNotFound should match itself via errors.Is")
	}
}
