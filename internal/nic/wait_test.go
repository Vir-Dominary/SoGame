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
	"testing"
	"time"
)

func TestWaitAdminStatusAlreadySatisfied(t *testing.T) {
	info := sampleAdapter(t)
	if err := WaitAdminStatus(context.Background(), info.Luid, info.AdminStatus, time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("WaitAdminStatus: %v", err)
	}
}

func TestWaitAdminStatusTimeout(t *testing.T) {
	info := sampleAdapter(t)
	err := WaitAdminStatus(context.Background(), info.Luid, ^uint32(0), time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestWaitAdminStatusInvalidLuidTimeout(t *testing.T) {
	err := WaitAdminStatus(context.Background(), ^uint64(0), AdminUp, time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func sampleAdapter(t *testing.T) Info {
	t.Helper()

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Skip("no adapters to test against")
	}
	return list[0]
}
