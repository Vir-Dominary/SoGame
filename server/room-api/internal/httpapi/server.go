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

package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sogame/server/room-api/internal/audit"
	"sogame/server/room-api/internal/rooms"
	"strings"
	"sync"
	"time"
)

type Config struct {
	AdminToken           string
	MaxBodyBytes         int64
	CreateRatePerMinute  int
	JoinRatePerMinute    int
	PeerRatePerMinute    int
	ProvisionConcurrency int
}

type Server struct {
	rooms       *rooms.Service
	cfg         Config
	createLimit *limiter
	joinLimit   *limiter
	peerLimit   *limiter
	provision   chan struct{}
}

func New(service *rooms.Service, cfg Config) *Server {
	return &Server{
		rooms:       service,
		cfg:         cfg,
		createLimit: newLimiter(cfg.CreateRatePerMinute),
		joinLimit:   newLimiter(cfg.JoinRatePerMinute),
		peerLimit:   newLimiter(cfg.PeerRatePerMinute),
		provision:   make(chan struct{}, cfg.ProvisionConcurrency),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		audit.Event("http_request", map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      status.status,
			"duration_ms": time.Since(started).Milliseconds(),
			"remote":      remoteIP(r),
		})
	}()

	if r.URL.Path == "/healthz" || r.URL.Path == "/rooms/healthz" {
		writeJSON(status, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/rooms" {
		s.createRoom(status, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/rooms/join" {
		s.joinRoom(status, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/rooms/") {
		s.roomAction(status, r)
		return
	}
	http.NotFound(status, r)
}

func (s *Server) createRoom(w *statusWriter, r *http.Request) {
	if r.ContentLength > s.cfg.MaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	if !s.createLimit.Allow(remoteIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	select {
	case s.provision <- struct{}{}:
		defer func() { <-s.provision }()
	default:
		writeError(w, http.StatusServiceUnavailable, "room provisioning busy")
		return
	}
	idempotency := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotency) > 128 {
		writeError(w, http.StatusBadRequest, "invalid idempotency key")
		return
	}
	response, err := s.rooms.Create(r.Context(), idempotency)
	if err != nil {
		if errors.Is(err, rooms.ErrOperationInProgress) {
			writeError(w, http.StatusConflict, "room operation in progress")
			return
		}
		log.Printf("room creation failed: %v", err)
		writeError(w, http.StatusBadGateway, "room provisioning failed")
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) joinRoom(w *statusWriter, r *http.Request) {
	if !s.joinLimit.Allow(remoteIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var request struct {
		RoomCode string `json:"room_code"`
	}
	if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &request) || request.RoomCode == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	response, err := s.rooms.Join(r.Context(), request.RoomCode)
	if errors.Is(err, rooms.ErrRoomClosed) {
		writeError(w, http.StatusGone, "room_closed")
		return
	}
	if errors.Is(err, rooms.ErrInvalidRoom) {
		writeError(w, http.StatusNotFound, "room unavailable")
		return
	}
	if err != nil {
		log.Printf("room join failed: %v", err)
		writeError(w, http.StatusBadGateway, "room unavailable")
		return
	}
	response.RoomCode = ""
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) roomAction(w *statusWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[1] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	code, action := parts[1], parts[2]
	switch {
	case r.Method == http.MethodGet && action == "peers":
		if !s.peerLimit.Allow(remoteIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		response, err := s.rooms.Peers(r.Context(), code)
		if errors.Is(err, rooms.ErrRoomClosed) {
			writeError(w, http.StatusGone, "room_closed")
			return
		}
		if errors.Is(err, rooms.ErrInvalidRoom) {
			writeError(w, http.StatusNotFound, "room unavailable")
			return
		}
		if err != nil {
			log.Printf("peer listing failed: %v", err)
			writeError(w, http.StatusBadGateway, "peer listing unavailable")
			return
		}
		writeJSON(w, http.StatusOK, response)
	case r.Method == http.MethodPost && action == "close":
		// 房主解散房间（标题"房主离开即断开"）。令牌与房间码同强度；挂在 join 限流下。
		if !s.joinLimit.Allow(remoteIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		var request struct {
			OwnerToken string `json:"owner_token"`
		}
		if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &request) || strings.TrimSpace(request.OwnerToken) == "" {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		err := s.rooms.Close(r.Context(), code, request.OwnerToken)
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(err, rooms.ErrCloseForbidden) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if errors.Is(err, rooms.ErrInvalidRoom) {
			writeError(w, http.StatusNotFound, "room unavailable")
			return
		}
		log.Printf("room close failed: %v", err)
		writeError(w, http.StatusBadGateway, "room close failed")
	case r.Method == http.MethodPost && action == "heartbeat":
		// 房主心跳：证明房主在线，避免被看门狗清扫。挂在 peer 限流档位。
		if !s.peerLimit.Allow(remoteIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		var request struct {
			OwnerToken string `json:"owner_token"`
		}
		if !decodeJSON(w, r, s.cfg.MaxBodyBytes, &request) || strings.TrimSpace(request.OwnerToken) == "" {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		err := s.rooms.Heartbeat(r.Context(), code, request.OwnerToken)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, rooms.ErrRoomClosed):
			writeError(w, http.StatusGone, "room_closed")
		case errors.Is(err, rooms.ErrCloseForbidden):
			writeError(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, rooms.ErrInvalidRoom):
			writeError(w, http.StatusNotFound, "room unavailable")
		default:
			log.Printf("room heartbeat failed: %v", err)
			writeError(w, http.StatusBadGateway, "room heartbeat failed")
		}
	case r.Method == http.MethodPost && action == "disable":
		if !s.authorizedAdmin(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := s.rooms.Disable(r.Context(), code); errors.Is(err, rooms.ErrInvalidRoom) {
			writeError(w, http.StatusNotFound, "room unavailable")
		} else if err != nil {
			log.Printf("room disable failed: %v", err)
			writeError(w, http.StatusBadGateway, "room disable failed")
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) authorizedAdmin(r *http.Request) bool {
	if s.cfg.AdminToken == "" {
		return false
	}
	provided := []byte(r.Header.Get("X-Room-Admin-Token"))
	wanted := []byte(s.cfg.AdminToken)
	return len(provided) == len(wanted) && subtle.ConstantTimeCompare(provided, wanted) == 1
}

func decodeJSON(w http.ResponseWriter, r *http.Request, max int64, target any) bool {
	if max <= 0 {
		max = 4096
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("response encoding failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func remoteIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]counter
}

type counter struct {
	started time.Time
	count   int
}

func newLimiter(limit int) *limiter {
	if limit < 1 {
		limit = 1
	}
	return &limiter{limit: limit, window: time.Minute, clients: make(map[string]counter)}
}

func (l *limiter) Allow(client string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.clients[client]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		l.clients[client] = counter{started: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.clients[client] = entry
	return true
}