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
	"net/http"
	"testing"

	"sogame/internal/securestore"
)

func TestSwitchRequiresConfirmationAndValidMode(t *testing.T) {
	service, _, _ := savedSessionService(t, &fakeSessionAdapter{fail: map[string]error{}})
	if _, err := service.Switch(context.Background(), SwitchRequest{Mode: "create", Hostname: "pc"}); !errors.Is(err, ErrSwitchConfirmationRequired) {
		t.Fatalf("unconfirmed switch error=%v", err)
	}
	if _, err := service.Switch(context.Background(), SwitchRequest{Mode: "other", Hostname: "pc", Confirmed: true}); !errors.Is(err, ErrInvalidSwitchMode) {
		t.Fatalf("invalid mode error=%v", err)
	}
}

func TestSwitchCompletesLeaveBeforeStartingNewCreate(t *testing.T) {
	apiCalls := 0
	rooms, server := newSessionRoomAPI(t, func(response http.ResponseWriter, request *http.Request) {
		apiCalls++
		successfulRoomHandler(t)(response, request)
	})
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	adapter.status.ManagementConnected = true
	adapter.status.SignalConnected = true
	service, metadata, codes := savedSessionService(t, adapter)
	service.rooms = rooms

	snapshot, err := service.Switch(context.Background(), SwitchRequest{Mode: "create", Hostname: "new-pc", Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateControlPlaneConnected || apiCalls != 1 {
		t.Fatalf("snapshot=%+v apiCalls=%d", snapshot, apiCalls)
	}
	if len(adapter.calls) < 5 || adapter.calls[0] != "deregister" || adapter.calls[1] != "remove-profile" || adapter.calls[2] != "create-profile" || adapter.calls[3] != "enroll" || adapter.calls[4] != "connect" {
		t.Fatalf("switch calls=%v", adapter.calls)
	}
	if metadata.value == nil || string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatalf("new room was not committed: metadata=%+v code=%q", metadata.value, codes.value)
	}
}

func TestSwitchLeavesOldRoomBeforeJoiningNewRoom(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, _, _ := savedSessionService(t, adapter)
	service.rooms = rooms

	if _, err := service.Switch(context.Background(), SwitchRequest{Mode: "join", RoomCode: "AAAA-BBBB-CCCC", Hostname: "new-pc", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.calls) < 5 || adapter.calls[0] != "deregister" || adapter.calls[1] != "remove-profile" {
		t.Fatalf("switch calls=%v", adapter.calls)
	}
}

func TestSwitchDoesNotStartNewRoomAfterLeaveFailure(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{"remove-profile": errors.New("remove failed")}}
	service, _, _ := savedSessionService(t, adapter)
	service.rooms = rooms

	_, err := service.Switch(context.Background(), SwitchRequest{Mode: "create", Hostname: "new-pc", Confirmed: true})
	if err == nil || containsCall(adapter.calls, "create-profile") {
		t.Fatalf("switch continued after leave failure: error=%v calls=%v", err, adapter.calls)
	}
}

func TestSwitchWithoutSavedRoomStartsNewRoom(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})

	if _, err := service.Switch(context.Background(), SwitchRequest{Mode: "create", Hostname: "new-pc", Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if containsCall(adapter.calls, "deregister") || containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("no-room switch performed leave: %v", adapter.calls)
	}
	if _, err := service.metadata.Load(); err != nil && !errors.Is(err, securestore.ErrNoRoomMetadata) {
		t.Fatal(err)
	}
}
