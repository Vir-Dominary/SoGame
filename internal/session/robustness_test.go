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

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

func savedRoomMetadata() *memoryMetadata {
	return &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     time.Now().UTC().Add(-time.Hour),
	}}
}

// roomViewService 构建一个已保存房间、房间 API 返回一名在线成员的会话服务,
// 用于验证对等体等待计时与状态降级。
func roomViewService(t *testing.T) (*Service, *fakeSessionAdapter, clientnetbird.Snapshot) {
	t.Helper()
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	rooms := &viewRoomAPI{peers: roomapi.PeerList{RoomID: "room-1", Peers: []roomapi.Peer{{ID: "peer-2", NetBirdIP: "100.115.10.22"}}}}
	adapter := &fakeSessionAdapter{status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true}}
	service := NewService(rooms, adapter, savedRoomMetadata(), codes)
	return service, adapter, clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true}
}

func TestPeerConnectionWaitTimesOutToReconnecting(t *testing.T) {
	service, _, _ := roomViewService(t)
	start := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return start }

	first, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Session.State != StateConnectingPeer {
		t.Fatalf("expected peer connection in progress, got %+v", first.Session)
	}

	service.now = func() time.Time { return start.Add(peerWaitTimeout + time.Second) }
	second, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Session.State != StateReconnecting {
		t.Fatalf("expected reconnecting after peer wait timeout, got %+v", second.Session)
	}
}

func TestPeerConnectionProgressClearsWaitTimer(t *testing.T) {
	service, adapter, _ := roomViewService(t)
	start := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return start }
	if _, err := service.View(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 房间成员等待超时后,守护进程出现对端,计时应归零,状态回到 P2P 连接。
	service.now = func() time.Time { return start.Add(peerWaitTimeout + time.Second) }
	adapter.status = clientnetbird.Snapshot{
		ManagementConnected: true,
		SignalConnected:     true,
		Peers:               []clientnetbird.Peer{{NetBirdIP: "100.115.10.21", State: clientnetbird.PeerConnected, Path: clientnetbird.PathP2P}},
	}
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Session.State != StateConnectedP2P {
		t.Fatalf("expected connected P2P after peer arrival, got %+v", view.Session)
	}
}

func TestDisconnectIntentSurvivesElapsedPeerWait(t *testing.T) {
	service, adapter, _ := roomViewService(t)
	if _, err := service.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return start.Add(peerWaitTimeout + time.Second) }
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Session.State != StateControlPlaneConnected {
		t.Fatalf("disconnected room must stay ControlPlaneConnected, got %+v", view.Session)
	}
	if !containsCall(adapter.calls, "disconnect") {
		t.Fatalf("disconnect adapter call missing: %v", adapter.calls)
	}
}

func TestLeaveIsIdempotentWhenProfileAlreadyGone(t *testing.T) {
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{
		fail: map[string]error{
			"deregister": clientnetbird.ErrManagedProfileInconsistent,
			"remove-profile": clientnetbird.ErrManagedProfileInconsistent,
		},
	}
	service := NewService(nil, adapter, savedRoomMetadata(), codes)
	snapshot, err := service.Leave(context.Background())
	if err != nil {
		t.Fatalf("leave with gone profile must not fail: %v", err)
	}
	if snapshot.State != StateNoRoom {
		t.Fatalf("snapshot=%+v, want NoRoom", snapshot)
	}
	if codes.value != nil {
		t.Fatalf("room code must be cleared, got %q", codes.value)
	}
}

func TestLeaveFailsOnOtherDeregisterErrors(t *testing.T) {
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{fail: map[string]error{"deregister": errors.New("daemon unreachable")}}
	service := NewService(nil, adapter, savedRoomMetadata(), codes)
	snapshot, err := service.Leave(context.Background())
	if err == nil {
		t.Fatal("leave must fail on unrelated deregister errors")
	}
	if snapshot.State != StateRecoverableError {
		t.Fatalf("snapshot=%+v, want RecoverableError", snapshot)
	}
}

func TestCountDistinctPeersMergesByIP(t *testing.T) {
	daemon := []clientnetbird.Peer{
		{NetBirdIP: "100.115.10.21"},
		{NetBirdIP: "100.115.10.22"},
		{FQDN: "peer-3.example.com"},
	}
	members := []roomapi.Peer{
		{ID: "p1", NetBirdIP: "100.115.10.22"},
		{ID: "p2", NetBirdIP: "100.115.10.23"},
		{ID: "p3"},
	}
	if got := countDistinctPeers(daemon, members, "100.115.10.10"); got != 5 {
		t.Fatalf("countDistinctPeers=%d, want 5", got)
	}
}

func TestCountDistinctPeersExcludesLocalIP(t *testing.T) {
	daemon := []clientnetbird.Peer{
		{NetBirdIP: "100.115.10.21"},
	}
	members := []roomapi.Peer{
		{ID: "p1", NetBirdIP: "100.115.10.21"},
	}
	if got := countDistinctPeers(daemon, members, "100.115.10.21"); got != 0 {
		t.Fatalf("countDistinctPeers=%d, want 0", got)
	}
}

func TestCountDistinctPeersNormalizesCIDRLocalIP(t *testing.T) {
	daemon := []clientnetbird.Peer{}
	members := []roomapi.Peer{
		{ID: "p1", NetBirdIP: "100.66.172.6"},
	}
	if got := countDistinctPeers(daemon, members, "100.66.172.6/16"); got != 0 {
		t.Fatalf("countDistinctPeers=%d, want 0 (local IP with CIDR must match)", got)
	}
}

func TestExcludeLocalPeerNormalizesCIDR(t *testing.T) {
	members := []roomapi.Peer{
		{ID: "self", NetBirdIP: "100.66.172.6"},
		{ID: "other", NetBirdIP: "100.66.99.99"},
	}
	filtered := excludeLocalPeer(members, "100.66.172.6/16")
	if len(filtered) != 1 || filtered[0].ID != "other" {
		t.Fatalf("excludeLocalPeer=%+v, want only other", filtered)
	}
}

func TestValidationFailuresDoNotRewriteSessionState(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service := NewService(nil, adapter, &memoryMetadata{}, &memoryRoomCode{})

	longName := ""
	for i := 0; i < 64; i++ {
		longName += "x"
	}
	for _, name := range []string{"", "   ", longName} {
		snapshot, err := service.Create(context.Background(), name)
		if err == nil {
			t.Fatalf("hostname %q: expected validation error", name)
		}
		if snapshot.State != StateNoRoom {
			t.Fatalf("hostname %q: validation rewrote state to %s", name, snapshot.State)
		}
	}
	if len(adapter.calls) != 0 {
		t.Fatalf("validation triggered daemon calls: %v", adapter.calls)
	}

	snapshot, err := service.Disconnect(context.Background())
	if !errors.Is(err, securestore.ErrNoRoomMetadata) || snapshot.State != StateNoRoom {
		t.Fatalf("disconnect without saved room: snapshot=%+v error=%v", snapshot, err)
	}
	if len(adapter.calls) != 0 {
		t.Fatalf("validation triggered daemon calls: %v", adapter.calls)
	}
}

func TestLeaveWithOrphanRoomCodeForceClears(t *testing.T) {
	// 只有 room code 没有 metadata:不可恢复的不一致状态,Leave 应强制清理。
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service := NewService(nil, adapter, &memoryMetadata{}, codes)

	snapshot, err := service.Leave(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateNoRoom || codes.value != nil {
		t.Fatalf("snapshot=%+v code=%q", snapshot, codes.value)
	}
}

func TestForceLeaveRestoresNoRoomFromConsistentState(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, metadata, codes := savedSessionService(t, adapter)

	snapshot, err := service.ForceLeave(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateNoRoom || metadata.value != nil || codes.value != nil {
		t.Fatalf("snapshot=%+v metadata=%v code=%q", snapshot, metadata.value, codes.value)
	}
	if !containsCall(adapter.calls, "deregister") || !containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("force leave must deregister and remove the profile: %v", adapter.calls)
	}
}