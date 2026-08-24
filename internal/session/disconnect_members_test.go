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
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

// selfMemberRoom 返回包含"自己"在内的房间成员列表(单人间)。
func selfMemberRoom(ownIP string) roomapi.PeerList {
	return roomapi.PeerList{
		RoomID: "room-1",
		Peers: []roomapi.Peer{
			{ID: "self", Name: "virdy", NetBirdIP: ownIP},
		},
	}
}

func TestSelfRemainsExcludedAfterDisconnectWithoutDaemonIP(t *testing.T) {
	ownIP := "100.66.1.1"
	metadata := &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     time.Now().UTC().Add(-time.Hour),
	}}
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{
		status: clientnetbird.Snapshot{
			ManagementConnected: true,
			SignalConnected:     true,
			LocalNetBirdIP:      ownIP + "/16",
		},
	}
	rooms := &viewRoomAPI{peers: selfMemberRoom(ownIP)}
	service := NewService(rooms, adapter, metadata, codes)

	// 在线时:自己按虚拟 IP 被排除,成员列表应为空
	online, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(online.Peers) != 0 {
		t.Fatalf("online view must not include self, got %+v", online.Peers)
	}
	if online.Session.State != StateWaitingForPeer {
		t.Fatalf("online state=%s, want WaitingForPeer", online.Session.State)
	}

	// 用户点击"断开"后 daemon 不再报告本机 IP,成员列表仍不应包含自己
	if _, err := service.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	adapter.status = clientnetbird.Snapshot{
		ManagementConnected: true,
		SignalConnected:     true,
	}
	offline, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(offline.Peers) != 0 {
		t.Fatalf("disconnected view must still exclude self, got %+v", offline.Peers)
	}
	if offline.Session.State != StateControlPlaneConnected {
		t.Fatalf("disconnected state=%s, want ControlPlaneConnected", offline.Session.State)
	}
}

func TestOthersStillVisibleAfterDisconnectWithoutDaemonIP(t *testing.T) {
	ownIP := "100.66.1.1"
	metadata := &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     time.Now().UTC().Add(-time.Hour),
	}}
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{
		status: clientnetbird.Snapshot{
			ManagementConnected: true,
			SignalConnected:     true,
			LocalNetBirdIP:      ownIP + "/16",
		},
	}
	rooms := &viewRoomAPI{peers: roomapi.PeerList{
		RoomID: "room-1",
		Peers: []roomapi.Peer{
			{ID: "self", Name: "virdy", NetBirdIP: ownIP},
			{ID: "friend", Name: "alice", NetBirdIP: "100.66.2.2"},
		},
	}}
	service := NewService(rooms, adapter, metadata, codes)
	if _, err := service.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	adapter.status = clientnetbird.Snapshot{
		ManagementConnected: true,
		SignalConnected:     true,
	}
	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Peers) != 1 || view.Peers[0].ID != "friend" {
		t.Fatalf("other members must remain visible after disconnect, got %+v", view.Peers)
	}
}

func TestLocalIPCacheDoesNotLeakAcrossRooms(t *testing.T) {
	ownIP := "100.66.1.1"
	adapter := &fakeSessionAdapter{
		status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true, LocalNetBirdIP: ownIP + "/16"},
	}
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	service := NewService(rooms, adapter, savedRoomMetadata(), &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")})
	if _, err := service.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.lastLocalIP != ownIP {
		t.Fatalf("lastLocalIP=%q, want %q", service.lastLocalIP, ownIP)
	}
	// 离开后缓存必须清空,不能把旧 IP 带进新房间
	if _, err := service.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.lastLocalIP != "" {
		t.Fatalf("lastLocalIP must be cleared on leave, got %q", service.lastLocalIP)
	}
}