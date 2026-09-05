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
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"sogame/internal/securestore"
)

// ownerAwareRoomHandler 模拟支持房主机制的房间 API：
//   - Create 返回 owner_token
//   - 记录 close/heartbeat 的调用参数
//   - closed=true 时 peers 返回 410 room_closed
type ownerRoomServer struct {
	mu          sync.Mutex
	closed      bool
	closeCalls  []ownerCall
	heartbeats  []ownerCall
	peersBodies int
}

type ownerCall struct {
	code  string
	token string
}

func (s *ownerRoomServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		switch r.URL.Path {
		case "/rooms":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"room_id":"room-1","room_code":"AAAA-BBBB-CCCC","management_url":"https://legengen.top","setup_key":"secret-key","owner_token":"the-owner-token"}`))
		case "/rooms/join":
			if closed {
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"error":"room_closed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"room_id":"room-1","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/AAAA-BBBB-CCCC/close":
			var body struct {
				OwnerToken string `json:"owner_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.closeCalls = append(s.closeCalls, ownerCall{code: "AAAA-BBBB-CCCC", token: body.OwnerToken})
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/rooms/AAAA-BBBB-CCCC/heartbeat":
			var body struct {
				OwnerToken string `json:"owner_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.heartbeats = append(s.heartbeats, ownerCall{code: "AAAA-BBBB-CCCC", token: body.OwnerToken})
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/rooms/AAAA-BBBB-CCCC/peers":
			if closed {
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"error":"room_closed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"room_id":"room-1","peers":[]}`))
		default:
			http.NotFound(w, r)
		}
	}
}

// 房主令牌存储的内存实现（与 memoryRoomCode 同形,但语义是房主令牌）
type memoryOwnerToken struct {
	value []byte
}

func (m *memoryOwnerToken) Load() ([]byte, error) {
	if m.value == nil {
		return nil, securestore.ErrNoOwnerToken
	}
	return append([]byte(nil), m.value...), nil
}

func (m *memoryOwnerToken) Save(value []byte) error {
	m.value = append([]byte(nil), value...)
	return nil
}

func (m *memoryOwnerToken) Clear() error {
	m.value = nil
	return nil
}

func TestOwnerLeaveDissolvesRoomOnServer(t *testing.T) {
	backend := &ownerRoomServer{}
	rooms, server := newSessionRoomAPI(t, backend.handler())
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	metadata := &memoryMetadata{}
	codes := &memoryRoomCode{}
	tokens := &memoryOwnerToken{}
	service := NewService(rooms, adapter, metadata, codes)
	service.SetOwnerTokenStore(tokens)

	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tokens.value == nil {
		t.Fatal("owner token must be persisted after create")
	}

	if _, err := service.Leave(context.Background()); err != nil {
		t.Fatalf("leave: %v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.closeCalls) != 1 {
		t.Fatalf("expected exactly one close call, got %v", backend.closeCalls)
	}
	if backend.closeCalls[0].token != "the-owner-token" {
		t.Fatal("close must carry the persisted owner token")
	}
	if tokens.value != nil || metadata.value != nil || codes.value != nil {
		t.Fatal("owner leave must clear token/metadata/code locally")
	}
}

func TestMemberLeaveDoesNotCloseRoom(t *testing.T) {
	backend := &ownerRoomServer{}
	rooms, server := newSessionRoomAPI(t, backend.handler())
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})
	service.SetOwnerTokenStore(&memoryOwnerToken{})

	if _, err := service.Join(context.Background(), "AAAA-BBBB-CCCC", "joiner-pc"); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := service.Leave(context.Background()); err != nil {
		t.Fatalf("leave: %v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.closeCalls) != 0 {
		t.Fatalf("member leave must never call close, got %v", backend.closeCalls)
	}
}

func TestViewDetectsRoomClosedAndClearsLocalState(t *testing.T) {
	backend := &ownerRoomServer{}
	rooms, server := newSessionRoomAPI(t, backend.handler())
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	metadata := &memoryMetadata{}
	codes := &memoryRoomCode{}
	service := NewService(rooms, adapter, metadata, codes)
	service.SetOwnerTokenStore(&memoryOwnerToken{})

	if _, err := service.Create(context.Background(), "gaming-pc"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 房主在别处解散了房间 → 拉 peers 时应收到 410 room_closed
	backend.mu.Lock()
	backend.closed = true
	backend.mu.Unlock()

	_, err := service.View(context.Background())
	if !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("expected ErrRoomClosed, got %v", err)
	}
	if metadata.value != nil || codes.value != nil {
		t.Fatal("closed room must clear local room state")
	}
	// 后续视图应回到无房态(不再报错)
	if _, err := service.View(context.Background()); err != nil && !errors.Is(err, securestore.ErrNoRoomMetadata) {
		t.Fatalf("post-close view should behave like fresh state, got %v", err)
	}
}
