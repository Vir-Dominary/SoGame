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

// Room API 本地模拟服务
//
// 在本地启动一个最小化的 Room API HTTP 服务端，用于验证 SoGame
// 极速模式（NetBird）的完整创建房间、加入房间、查询成员流程。
// 房间 API 完全本地运行；Management 服务器默认指向已部署的
// https://legengen.top（与产品配置一致），可用 MOCK_MANAGEMENT 覆盖。
//
// 启动方式:
//   go run ./tools/room-api-mock/main.go
//
// 生成一次性 curl 示例:
//   go run ./tools/room-api-mock/main.go -demo
//
// 环境变量:
//   ROOM_API_ADDR    监听地址，默认 :9099
//   MOCK_MANAGEMENT   Management 服务器 URL (留给客户端用)，默认 https://legengen.top
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const roomCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var listenAddr = envOrDefault("ROOM_API_ADDR", "127.0.0.1:9099")
var mockManagementURL = envOrDefault("MOCK_MANAGEMENT", "https://legengen.top")

// mockRelayEnabled 模拟"服务器是否允许 Relay 中继"（由提供服务的服务器掌握）。
// 默认 false（纯 P2P 优先）；私有服务器联调时可设 MOCK_RELAY_ENABLED=true。
func mockRelayEnabled() bool {
	return envOrDefault("MOCK_RELAY_ENABLED", "false") == "true"
}

func main() {
	service := newMockService()
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           service,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		log.Printf("=== 本地 Mock Room API ===")
		log.Printf("地址:  http://%s", listenAddr)
		log.Printf("管理:  %s (客户端使用)", mockManagementURL)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func apiError(code string) map[string]string {
	return map[string]string{"error": code}
}

type mockService struct {
	mu    sync.RWMutex
	rooms map[string]*mockRoom
}

type mockRoom struct {
	ID        string
	Code      string
	SetupKey  string
	Status    string
	CreatedAt int64
	Peers     []mockPeer
}

type mockPeer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NetBirdIP string `json:"netbird_ip"`
	Connected bool   `json:"connected"`
	Hostname  string `json:"hostname,omitempty"`
}

func newMockService() *mockService {
	return &mockService{rooms: make(map[string]*mockRoom)}
}

func (s *mockService) hash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

func (s *mockService) join(body json.RawMessage) (int, any) {
	var request struct {
		RoomCode string `json:"room_code"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.RoomCode == "" {
		return 400, apiError("invalid_request")
	}
	s.mu.RLock()
	room, found := s.rooms[s.hash(request.RoomCode)]
	s.mu.RUnlock()
	if !found || room.Status != "active" {
		return 404, apiError("room_unavailable")
	}
	return 200, struct {
		RoomID        string `json:"room_id"`
		ManagementURL string `json:"management_url"`
		SetupKey      string `json:"setup_key"`
		RelayEnabled  bool   `json:"relay_enabled"`
	}{RoomID: room.ID, ManagementURL: mockManagementURL, SetupKey: room.SetupKey, RelayEnabled: mockRelayEnabled()}
}

func (s *mockService) create(ik string) (int, []byte, error) {
	code, err := genRoomCode()
	if err != nil {
		return 500, nil, fmt.Errorf("generate code: %w", err)
	}
	id := randomHex(12)
	key := randomHex(32)
	s.mu.Lock()
	s.rooms[s.hash(code)] = &mockRoom{
		ID: id, Code: code, SetupKey: key, Status: "active",
		CreatedAt: time.Now().UnixNano(),
		Peers:     []mockPeer{},
	}
	s.mu.Unlock()
	body, _ := json.Marshal(struct {
		RoomID        string `json:"room_id"`
		RoomCode      string `json:"room_code"`
		ManagementURL string `json:"management_url"`
		SetupKey      string `json:"setup_key"`
		RelayEnabled  bool   `json:"relay_enabled"`
	}{RoomID: id, RoomCode: code, ManagementURL: mockManagementURL, SetupKey: key, RelayEnabled: mockRelayEnabled()})
	return 201, body, nil
}

func (s *mockService) peers(code string) (int, any, error) {
	s.mu.RLock()
	room, found := s.rooms[s.hash(code)]
	s.mu.RUnlock()
	if !found || room.Status != "active" {
		return 404, apiError("room_unavailable"), nil
	}
	return 200, struct {
		RoomID string     `json:"room_id"`
		Peers  []mockPeer `json:"peers"`
	}{RoomID: room.ID, Peers: room.Peers}, nil
}

// HTTP 处理
func (s *mockService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/rooms":
		ik := r.Header.Get("Idempotency-Key")
		status, data, err := s.create(ik)
		if err != nil {
			writeJSON(w, status, data)
			return
		}
		w.WriteHeader(status)
		w.Write(data)

	case r.Method == http.MethodPost && r.URL.Path == "/rooms/join":
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		status, response := s.join(body)
		writeJSON(w, status, response)

	case r.Method == http.MethodGet && r.URL.Path == "/debug/rooms":
		s.mu.RLock()
		rooms := make([]*mockRoom, 0, len(s.rooms))
		for _, room := range s.rooms {
			rooms = append(rooms, room)
		}
		s.mu.RUnlock()
		writeJSON(w, 200, rooms)

	case strings.HasPrefix(r.URL.Path, "/rooms/"):
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 3 && parts[2] == "peers" {
			status, response, _ := s.peers(parts[1])
			writeJSON(w, status, response)
			return
		}
		http.NotFound(w, r)

	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func genRoomCode() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = roomCharset[int(buf[i])%len(roomCharset)]
	}
	return fmt.Sprintf("%s-%s-%s", string(buf[:4]), string(buf[4:8]), string(buf[8:])), nil
}