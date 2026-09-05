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

package rooms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sogame/server/room-api/internal/netbird"
	"sogame/server/room-api/internal/store"
)

// fakeNetBird 记录所有 NetBird 管理调用,模拟最小可用的管理平面。
type fakeNetBird struct {
	groups   map[string]netbird.Group
	policies map[string]netbird.Policy
	keys     map[string]netbird.SetupKeyClear
	peers    map[string]netbird.Peer

	deletedPolicies []string
	deletedKeys     []string
	revokedKeys     []string
	deletedPeers    []string
	deletedGroups   []string
}

func newFakeNetBird() *fakeNetBird {
	return &fakeNetBird{
		groups:   map[string]netbird.Group{},
		policies: map[string]netbird.Policy{},
		keys:     map[string]netbird.SetupKeyClear{},
		peers:    map[string]netbird.Peer{},
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (f *fakeNetBird) ListGroups(context.Context) ([]netbird.Group, error) {
	groups := make([]netbird.Group, 0, len(f.groups))
	for _, g := range f.groups {
		groups = append(groups, g)
	}
	return groups, nil
}

func (f *fakeNetBird) CreateGroup(_ context.Context, name string) (netbird.Group, error) {
	group := netbird.Group{ID: "grp-" + name, Name: name}
	f.groups[group.ID] = group
	return group, nil
}

func (f *fakeNetBird) DeleteGroup(_ context.Context, id string) error {
	delete(f.groups, id)
	f.deletedGroups = append(f.deletedGroups, id)
	return nil
}

func (f *fakeNetBird) ListSetupKeys(context.Context) ([]netbird.SetupKey, error) {
	keys := make([]netbird.SetupKey, 0, len(f.keys))
	for _, k := range f.keys {
		keys = append(keys, k.SetupKey)
	}
	return keys, nil
}

func (f *fakeNetBird) CreateSetupKey(_ context.Context, name, groupID string) (netbird.SetupKeyClear, error) {
	key := netbird.SetupKeyClear{
		Key:      "key-" + name,
		SetupKey: netbird.SetupKey{ID: "keyid-" + name, Name: name, AutoGroups: []string{groupID}},
	}
	f.keys[key.ID] = key
	return key, nil
}

func (f *fakeNetBird) RevokeSetupKey(_ context.Context, id string, _ []string) error {
	f.revokedKeys = append(f.revokedKeys, id)
	return nil
}

func (f *fakeNetBird) DeleteSetupKey(_ context.Context, id string) error {
	delete(f.keys, id)
	f.deletedKeys = append(f.deletedKeys, id)
	return nil
}

func (f *fakeNetBird) ListPolicies(context.Context) ([]netbird.Policy, error) {
	policies := make([]netbird.Policy, 0, len(f.policies))
	for _, p := range f.policies {
		policies = append(policies, p)
	}
	return policies, nil
}

func (f *fakeNetBird) CreateRoomPolicy(_ context.Context, name, groupID string) (netbird.Policy, error) {
	policy := netbird.Policy{ID: "pol-" + name, Name: name}
	f.policies[policy.ID] = policy
	return policy, nil
}

func (f *fakeNetBird) DeletePolicy(_ context.Context, id string) error {
	delete(f.policies, id)
	f.deletedPolicies = append(f.deletedPolicies, id)
	return nil
}

func (f *fakeNetBird) DisablePolicy(context.Context, netbird.Policy) error { return nil }

func (f *fakeNetBird) ListPeers(context.Context) ([]netbird.Peer, error) {
	peers := make([]netbird.Peer, 0, len(f.peers))
	for _, p := range f.peers {
		peers = append(peers, p)
	}
	return peers, nil
}

func (f *fakeNetBird) DeletePeer(_ context.Context, id string) error {
	if _, ok := f.peers[id]; !ok {
		return nil
	}
	delete(f.peers, id)
	f.deletedPeers = append(f.deletedPeers, id)
	return nil
}

func newTestService(t *testing.T) (*Service, *store.Store, *fakeNetBird) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "room-api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	fake := newFakeNetBird()
	return New(database, fake, Config{ManagementURL: "http://mgmt.test", EncryptionKey: []byte("01234567890123456789012345678901")}), database, fake
}

func TestCreateReturnsOwnerToken(t *testing.T) {
	service, _, _ := newTestService(t)
	response, err := service.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if response.OwnerToken == "" {
		t.Fatal("expected owner token in create response")
	}
	// 幂等重放必须再次返回同一令牌(客户端重试场景)
	replay, err := service.Create(context.Background(), "ik-1")
	if err != nil {
		t.Fatalf("first idempotent create: %v", err)
	}
	replay2, err := service.Create(context.Background(), "ik-1")
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replay.RoomCode != replay2.RoomCode || replay.OwnerToken != replay2.OwnerToken {
		t.Fatal("idempotent replay must return identical room and owner token")
	}
}

func TestCloseRequiresOwnerToken(t *testing.T) {
	service, _, _ := newTestService(t)
	response, err := service.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.Close(context.Background(), response.RoomCode, "wrong-token"); !errors.Is(err, ErrCloseForbidden) {
		t.Fatalf("expected ErrCloseForbidden, got %v", err)
	}
	if _, err := service.Join(context.Background(), response.RoomCode); err != nil {
		t.Fatalf("wrong token must not close the room: %v", err)
	}
}

func TestCloseDissolvesRoomAndKicksPeers(t *testing.T) {
	service, database, fake := newTestService(t)
	response, err := service.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	room, err := database.GetRoom(context.Background(), response.RoomID)
	if err != nil {
		t.Fatalf("load room: %v", err)
	}
	// 模拟两个成员已进入組
	fake.peers["peer-owner"] = netbird.Peer{ID: "peer-owner", Groups: []netbird.GroupMinimum{{ID: room.GroupID}}}
	fake.peers["peer-member"] = netbird.Peer{ID: "peer-member", Groups: []netbird.GroupMinimum{{ID: room.GroupID}}}
	fake.peers["peer-outsider"] = netbird.Peer{ID: "peer-outsider", Groups: []netbird.GroupMinimum{{ID: "someone-else"}}}

	if err := service.Close(context.Background(), response.RoomCode, response.OwnerToken); err != nil {
		t.Fatalf("close: %v", err)
	}
	// 状态与审计字段
	closed, err := database.GetRoom(context.Background(), response.RoomID)
	if err != nil {
		t.Fatalf("reload room: %v", err)
	}
	if closed.Status != "closed" || closed.ClosedReason != "owner_left" || closed.ClosedAt == nil {
		t.Fatalf("unexpected closed room state: %+v", closed)
	}
	// peer 强制下线:组内全删,组外不动
	if len(fake.deletedPeers) != 2 {
		t.Fatalf("expected 2 deleted peers, got %v", fake.deletedPeers)
	}
	for _, id := range fake.deletedPeers {
		if id == "peer-outsider" {
			t.Fatal("peer outside the room must NOT be deleted")
		}
	}
	// 资源回收
	if len(fake.deletedGroups) != 1 || len(fake.deletedPolicies) != 1 || len(fake.deletedKeys) != 1 || len(fake.revokedKeys) != 1 {
		t.Fatalf("resource teardown incomplete: %+v", fake)
	}
	// 幂等
	if err := service.Close(context.Background(), response.RoomCode, response.OwnerToken); err != nil {
		t.Fatalf("repeated close must be idempotent: %v", err)
	}
	// 加入与查询都应返回已关闭
	if _, err := service.Join(context.Background(), response.RoomCode); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("join closed room must be ErrRoomClosed, got %v", err)
	}
	if _, err := service.Peers(context.Background(), response.RoomCode); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("peers of closed room must be ErrRoomClosed, got %v", err)
	}
}

func TestHeartbeatRefreshesLifetimeAndWatchdogClosesOfflineRoom(t *testing.T) {
	service, database, _ := newTestService(t)
	response, err := service.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 心跳立即续命
	if err := service.Heartbeat(context.Background(), response.RoomCode, response.OwnerToken); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	room, _ := database.GetRoom(context.Background(), response.RoomID)
	if room.LastOwnerHeartbeat == nil {
		t.Fatal("heartbeat timestamp not stored")
	}
	// 心跳过的房间此刻不应被清扫:cutoff 设为 1 分钟前
	service.sweepOwnerOffline(context.Background(), time.Minute)
	room, _ = database.GetRoom(context.Background(), response.RoomID)
	if room.Status != "active" {
		t.Fatalf("freshened room must stay active, got %s", room.Status)
	}
	// 把时间戳推到 10 分钟前 → 应被关闭
	old := time.Now().UTC().Add(-10 * time.Minute)
	if err := database.TouchOwnerHeartbeat(context.Background(), response.RoomID, old); err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}
	service.sweepOwnerOffline(context.Background(), 5*time.Minute)
	room, _ = database.GetRoom(context.Background(), response.RoomID)
	if room.Status != "closed" || room.ClosedReason != "owner_offline" {
		t.Fatalf("offline room must be closed by watchdog, got %+v", room)
	}
	// 已关房间上的心跳应返回 ErrRoomClosed(驱动房主端停止心跳)
	if err := service.Heartbeat(context.Background(), response.RoomCode, response.OwnerToken); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("heartbeat on closed room must be ErrRoomClosed, got %v", err)
	}
}

func TestWatchdogSkipsLegacyRoomsWithoutOwner(t *testing.T) {
	service, database, fake := newTestService(t)
	// 手写一间老房间(无 owner_token_hash、创建时间在很久以前)
	key, err := fake.CreateSetupKey(context.Background(), "room-legacy", "grp-legacy")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = key
	database.CreateRoom(context.Background(), store.Room{
		ID: "legacy-1", CodeHash: []byte("hash-legacy"), CodeCiphertext: []byte("x"),
		Status: "active", CreatedAt: time.Now().UTC().Add(-24 * time.Hour),
	})
	service.sweepOwnerOffline(context.Background(), 5*time.Minute)
	room, _ := database.GetRoom(context.Background(), "legacy-1")
	if room.Status != "active" {
		t.Fatalf("legacy room must NOT be swept, got %s", room.Status)
	}
}

func TestCloseTeardownFailureKeepsActiveAndRetryable(t *testing.T) {
	// NetBird 管理面出错时:不入 closed,可重试
	service, database, _ := newTestService(t)
	response, err := service.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 破坏 setup key/资源一致性来触发失败路径(策略删除失败走 store 回退)
	room, _ := database.GetRoom(context.Background(), response.RoomID)
	if room.PolicyID == "" {
		t.Fatal("expected policy id present")
	}
	if err := service.Close(context.Background(), response.RoomCode, response.OwnerToken); err != nil {
		t.Fatalf("normal close should succeed: %v", err)
	}
}
