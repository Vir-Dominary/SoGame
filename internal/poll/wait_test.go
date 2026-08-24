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

package poll

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitUntilImmediate(t *testing.T) {
	calls := 0
	err := WaitUntil(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		calls++
		return true, nil
	}, "ready")
	if err != nil {
		t.Fatalf("WaitUntil: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestWaitUntilError(t *testing.T) {
	want := errors.New("boom")
	err := WaitUntil(context.Background(), time.Millisecond, 50*time.Millisecond, func() (bool, error) {
		return false, want
	}, "ready")
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestWaitUntilTimeout(t *testing.T) {
	err := WaitUntil(context.Background(), time.Millisecond, 5*time.Millisecond, func() (bool, error) {
		return false, nil
	}, "ready")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
