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

// package session_test 是 session 包的黑盒集成测试。
// 使用全 Mock 组件（假 Room API HTTP 服务器 + 假 NetBird 守护进程适配器 +
// 假安全存储）验证极速模式"创建房间"的完整端到端链路。
//
// 运行: go test -v -count=1 -run TestE2E ./internal/session/...
package session_test

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
	"sogame/internal/session"
)

// ---------------------------------------------------------------------------
// Mock Room API (httptest)
// ---------------------------------------------------------------------------

type mockRoomAPI struct {
	mu    sync.Mutex
	rooms map[string]mockRoom
}

type mockRoom struct {
	id   string
	code string
	key  string
}

func newMockRoomAPI() *mockRoomAPI {
	return &mockRoomAPI{rooms: make(map[string]mockRoom)}
}

func (a *mockRoomAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/rooms":
		code := randomRoomCode()
		id := randHex(12)
		key := randHex(32)
		a.mu.Lock()
		a.rooms[code] = mockRoom{id: id, code: code, key: key}
		a.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{
			"room_id":        id,
			"room_code":      code,
			"management_url": "https://localhost",
			"setup_key":      key,
		})
	case r.Method == "POST" && r.URL.Path == "/rooms/join":
		var body struct{ RoomCode string `json:"room_code"` }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			http.Error(w, "invalid_request", 400)
			return
		}
		a.mu.Lock()
		room, ok := a.rooms[body.RoomCode]
		a.mu.Unlock()
		if !ok {
			http.Error(w, "room_unavailable", 404)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"room_id":        room.id,
			"management_url": "https://localhost",
			"setup_key":      room.key,
		})
	case r.Method == "GET" && len(r.URL.Path) >= 6 && r.URL.Path[len(r.URL.Path)-6:] == "/peers":
		json.NewEncoder(w).Encode(map[string]any{"peers": []struct{}{}})
	default:
		http.NotFound(w, r)
	}
}

// ---------------------------------------------------------------------------
// Mock NetBird Adapter
// ---------------------------------------------------------------------------

type mockNetBirdAdapter struct {
	mu       sync.Mutex
	profiles map[string]*mockProfile
	snapshot mockDaemonSnapshot
}

type mockProfile struct {
	id      string
	name    string
	enrolled bool
	mgmtURL string
	host    string
}
type mockDaemonSnapshot struct {
	connected bool
	ip        string
}

func newMockAdapter() *mockNetBirdAdapter {
	return &mockNetBirdAdapter{
		profiles: make(map[string]*mockProfile),
		snapshot: mockDaemonSnapshot{connected: true, ip: "100.64.0.42"},
	}
}

func (m *mockNetBirdAdapter) DaemonVersion(context.Context) (string, error)      { return "0.74.7", nil }
func (m *mockNetBirdAdapter) Close() error                                     { return nil }
func (m *mockNetBirdAdapter) ListProfiles(context.Context) ([]clientnetbird.Profile, error) { return nil, nil }
func (m *mockNetBirdAdapter) SelectProfile(context.Context, string) error      { return nil }
func (m *mockNetBirdAdapter) ActiveProfile(context.Context) (clientnetbird.Profile, error) {
	return clientnetbird.Profile{}, clientnetbird.ErrManagedProfileInconsistent
}
func (m *mockNetBirdAdapter) RemoveProfile(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.profiles, id)
	m.mu.Unlock()
	return nil
}

func (m *mockNetBirdAdapter) Subscribe(_ context.Context) (<-chan clientnetbird.Event, <-chan error) {
	evts := make(chan clientnetbird.Event, 1)
	errs := make(chan error, 1)
	// 发送一个空事件表示启动，然后关闭 channel
	evts <- clientnetbird.Event{Category: "daemon"}
	close(evts)
	return evts, errs
}

func (m *mockNetBirdAdapter) Deregister(context.Context, string) error { return nil }

func (m *mockNetBirdAdapter) CreateProfile(_ context.Context, name string) (clientnetbird.Profile, error) {
	id := randHex(6)
	m.mu.Lock()
	m.profiles[id] = &mockProfile{id: id, name: name}
	m.mu.Unlock()
	return clientnetbird.Profile{ID: id, Name: name}, nil
}

func (m *mockNetBirdAdapter) Enroll(_ context.Context, req clientnetbird.EnrollmentRequest) error {
	defer req.SetupKey.Clear()
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.profiles[req.ProfileID]
	if !ok {
		return &clientnetbird.RPCError{Operation: "enroll", Code: codes.NotFound}
	}
	p.enrolled = true
	p.mgmtURL = req.ManagementURL
	p.host = req.Hostname
	m.snapshot.connected = true
	return nil
}

func (m *mockNetBirdAdapter) Connect(_ context.Context, profileID string) error {
	m.mu.Lock()
	if _, ok := m.profiles[profileID]; !ok {
		m.mu.Unlock()
		return &clientnetbird.RPCError{Operation: "connect", Code: codes.NotFound}
	}
	m.mu.Unlock()
	return nil
}

func (m *mockNetBirdAdapter) Disconnect(context.Context, string) error { return nil }

func (m *mockNetBirdAdapter) Status(context.Context) (clientnetbird.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.snapshot
	return clientnetbird.Snapshot{
		ManagementConnected: s.connected,
		SignalConnected:     s.connected,
		LocalNetBirdIP:      s.ip,
		Peers:               nil,
	}, nil
}

var _ clientnetbird.Adapter = (*mockNetBirdAdapter)(nil)

// ---------------------------------------------------------------------------
// Mock Storage (memory-backed)
// ---------------------------------------------------------------------------

type mockCodeStore struct{ data []byte }

func (s *mockCodeStore) Load() ([]byte, error) {
	if s.data == nil {
		return nil, securestore.ErrNoProtectedRoomCode
	}
	return append([]byte(nil), s.data...), nil
}
func (s *mockCodeStore) Save(v []byte) error { s.data = append([]byte(nil), v...); return nil }
func (s *mockCodeStore) Clear() error        { s.data = nil; return nil }

type mockMetaStore struct{ v *securestore.RoomMetadata }

func (s *mockMetaStore) Load() (securestore.RoomMetadata, error) {
	if s.v == nil {
		return securestore.RoomMetadata{}, securestore.ErrNoRoomMetadata
	}
	return *s.v, nil
}
func (s *mockMetaStore) Save(value securestore.RoomMetadata) error { s.v = &value; return nil }
func (s *mockMetaStore) Clear() error                              { s.v = nil; return nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func randHex(n int) string {
	b := make([]byte, n)
	cryptorand.Read(b)
	return hex.EncodeToString(b)
}

func randomRoomCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	cryptorand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:])
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestE2E_CreateRoom 验证完整创建房间链路：Room API → SetupKey → CreateProfile →
// Enroll → Connect → 安全存储。所有组件均为 mock，不需要外部服务或 Windows 守护进程。
func TestE2E_CreateRoom(t *testing.T) {
	// 1) 本地 mock HTTP 服务
	srv := httptest.NewServer(newMockRoomAPI())
	defer srv.Close()

	// 2) client + adapter + storage
	api, err := roomapi.NewClient(srv.URL, nil)
	if err != nil {
		t.Fatalf("Room API 客户端创建失败: %v", err)
	}
	adapter := newMockAdapter()
	meta := &mockMetaStore{}
	codes := &mockCodeStore{}

	// 3) session 服务
	svc := session.NewService(api, adapter, meta, codes)

	// 4) 创建房间
	snap, err := svc.Create(context.Background(), "TestPlayer")
	if err != nil {
		t.Fatalf("Create 方法返回错: %v", err)
	}

	if snap.State == session.StateRecoverableError {
		t.Fatalf("不期望 RecoverableError — 创建链路应该全部通过")
	}

	if snap.Revision == 0 {
		t.Error("期望 state machine revision 被推进")
	}
	t.Logf("创建成功: State=%s Path=%s Revision=%d", snap.State, snap.Path, snap.Revision)
}