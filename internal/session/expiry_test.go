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

package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"sogame/internal/securestore"
)

// expiredMetadata 返回一个创建于 roomMaxAge+1h 之前的过期房间。
func expiredMetadata() *memoryMetadata {
	created := time.Now().UTC().Add(-roomMaxAge - time.Hour)
	return &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-expired",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-expired",
		CreatedAt:     created,
	}}
}

func TestExpiredRoom(t *testing.T) {
	meta := securestore.RoomMetadata{CreatedAt: time.Now().Add(-25 * time.Hour)}
	if !expiredRoom(meta, time.Now()) {
		t.Fatal("room created 25h ago must be expired")
	}
	fresh := securestore.RoomMetadata{CreatedAt: time.Now().Add(-time.Hour)}
	if expiredRoom(fresh, time.Now()) {
		t.Fatal("room created 1h ago must still be valid")
	}
}

func TestLoadSavedRoomRejectsExpired(t *testing.T) {
	meta := expiredMetadata()
	service := NewService(nil, &fakeSessionAdapter{}, meta, &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	_, err := service.loadSavedRoom()
	if !errors.Is(err, ErrRoomExpired) {
		t.Fatalf("loadSavedRoom err=%v, want ErrRoomExpired", err)
	}
}

func TestSetResumePendingIgnoresExpiredRoom(t *testing.T) {
	service := NewService(nil, &fakeSessionAdapter{}, expiredMetadata(), &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	service.SetResumePending(true)
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.ResumePending {
		t.Fatal("expired room must not be offered for resume")
	}
	if view.Session.State != StateNoRoom {
		t.Fatalf("view=%s, want NoRoom", view.Session.State)
	}
}

func TestSetResumePendingOffersFreshRoomWithoutEnteringIt(t *testing.T) {
	service, adapter, _ := roomViewService(t)
	service.SetResumePending(true)

	// 待恢复状态下 View 必须保持 NoRoom,不进入房间界面、不触发重连
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !view.ResumePending {
		t.Fatal("fresh saved room must be offered for resume")
	}
	if view.Session.State != StateNoRoom {
		t.Fatalf("view=%s, want NoRoom while resume pending", view.Session.State)
	}
	if view.Metadata.RoomID != "" {
		t.Fatalf("room view must not expose room data while pending, got %q", view.Metadata.RoomID)
	}
	if len(adapter.calls) != 0 {
		t.Fatalf("no netbird calls allowed while resume pending, got %v", adapter.calls)
	}

	// 用户显式恢复后,才进入房间状态并执行恢复
	snapshot, err := service.Reconnect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State == StateNoRoom {
		t.Fatalf("reconnect must enter the room, got %+v", snapshot)
	}
	if !containsCall(adapter.calls, "connect") {
		t.Fatalf("reconnect adapter call missing: %v", adapter.calls)
	}
	view, err = service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.ResumePending {
		t.Fatal("resume pending must be cleared after explicit resume")
	}
}

func TestViewReportsNoRoomWhenExpiredOutsidePending(t *testing.T) {
	service := NewService(nil, &fakeSessionAdapter{}, expiredMetadata(), &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.ResumePending {
		t.Fatal("expired room must not be offered for resume")
	}
	if view.Session.State != StateNoRoom {
		t.Fatalf("view=%s, want NoRoom for expired room", view.Session.State)
	}
}

func TestReconnectRejectsExpiredRoom(t *testing.T) {
	service := NewService(nil, &fakeSessionAdapter{}, expiredMetadata(), &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	snapshot, err := service.Reconnect(context.Background())
	if !errors.Is(err, ErrRoomExpired) {
		t.Fatalf("reconnect err=%v, want ErrRoomExpired", err)
	}
	if snapshot.State != StateNoRoom {
		t.Fatalf("snapshot=%s, want NoRoom", snapshot.State)
	}
}

func TestCreateClearsExpiredRoomBeforeEnrolling(t *testing.T) {
	meta := expiredMetadata()
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &fakeSessionAdapter{}
	service := NewService(rooms, adapter, meta, &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	snapshot, err := service.Create(context.Background(), "host-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State == StateRecoverableError {
		t.Fatalf("create must proceed after clearing expired room, got %+v", snapshot)
	}
	if meta.value == nil || meta.value.ProfileID != "profile-1" {
		t.Fatalf("expired room must be replaced by the new enrollment, got %+v", meta.value)
	}
}

func TestCreateWhileResumePendingClearsOldRoom(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	meta := savedRoomMetadata()
	service := NewService(rooms, &fakeSessionAdapter{}, meta, codes)
	service.SetResumePending(true)

	snapshot, err := service.Create(context.Background(), "host-2")
	if err != nil {
		t.Fatalf("create while resume pending must proceed: %v", err)
	}
	if snapshot.State == StateRecoverableError {
		t.Fatalf("create must not fail while resume pending, got %+v", snapshot)
	}
	if meta.value == nil || meta.value.ProfileID != "profile-1" {
		t.Fatal("old room metadata must be replaced by the new enrollment")
	}
	if codes.value == nil || string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatal("old room code must be replaced by the new room code")
	}
}

func TestLeaveClearsExpiredRoom(t *testing.T) {
	meta := expiredMetadata()
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	service := NewService(nil, &fakeSessionAdapter{}, meta, codes)
	snapshot, err := service.Leave(context.Background())
	if err != nil {
		t.Fatalf("leave of expired room must succeed: %v", err)
	}
	if snapshot.State != StateNoRoom {
		t.Fatalf("snapshot=%s, want NoRoom", snapshot.State)
	}
	if meta.value != nil || codes.value != nil {
		t.Fatal("expired room files must be cleared on leave")
	}
}