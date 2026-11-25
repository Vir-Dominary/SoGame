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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sogame/server/room-api/internal/audit"
	"sogame/server/room-api/internal/rooms"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	AdminToken           string
	MaxBodyBytes         int64
	CreateRatePerMinute  int
	JoinRatePerMinute    int
	PeerRatePerMinute    int
	ProvisionConcurrency int
	// TrustProxy 表示请求是否经由可信反向代理（traefik/nginx）转发。
	// 为 true 时才信任 X-Forwarded-For 头用于限流计数与审计；
	// 为 false（默认）时只取 TCP 对端地址，防止公网直连下伪造头部绕过限流。
	TrustProxy bool
}

type Server struct {
	rooms       *rooms.Service
	cfg         Config
	createLimit *limiter
	joinLimit   *limiter
	peerLimit   *limiter
	provision   chan struct{}
	// notFoundCount 404 请求计数（采样审计用）。
	notFoundCount uint64
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
		// 404 请求（扫描器/垃圾路径，不经过任何限流器）按 1/64 采样审计：
		// 全量记录会被恶意请求洪泛成日志 DoS，并稀释真正的业务事件。
		if status.status == http.StatusNotFound && atomic.AddUint64(&s.notFoundCount, 1)%64 != 1 {
			return
		}
		audit.Event("http_request", map[string]any{
			"method":      r.Method,
			"path":        pathTemplate(r.URL.Path),
			"status":      status.status,
			"duration_ms": time.Since(started).Milliseconds(),
			"remote":      s.clientIP(r),
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
	// create 不读请求体（幂等键在 header），ContentLength 预检无防护对象。
	if !s.createLimit.Allow(rateLimitKey(s.clientIP(r))) {
		s.writeRateLimited(w, r, s.createLimit)
		return
	}
	// 幂等键校验先于信号量：信号量打满时，携带已完成幂等键的重放请求
	// 必须能拿到幂等响应，而不是被 503 挡回。
	idempotency := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotency) > 128 {
		writeError(w, http.StatusBadRequest, "invalid idempotency key")
		return
	}
	select {
	case s.provision <- struct{}{}:
		defer func() { <-s.provision }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "room provisioning busy")
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
	if r.ContentLength > s.cfg.MaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	if !s.joinLimit.Allow(rateLimitKey(s.clientIP(r))) {
		s.writeRateLimited(w, r, s.joinLimit)
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
		if !s.peerLimit.Allow(rateLimitKey(s.clientIP(r))) {
			s.writeRateLimited(w, r, s.peerLimit)
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
		if !s.joinLimit.Allow(rateLimitKey(s.clientIP(r))) {
			s.writeRateLimited(w, r, s.joinLimit)
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
		if !s.peerLimit.Allow(rateLimitKey(s.clientIP(r))) {
			s.writeRateLimited(w, r, s.peerLimit)
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
		case errors.Is(err, rooms.ErrOperationInProgress):
			// teardown 进行中（closing）：409 让房主客户端退避重试而非停跳，
			// teardown 失败回滚 active 后房间仍可继续服役。
			writeError(w, http.StatusConflict, "room teardown in progress")
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
		// 管理端点挂限流并留审计：鉴权失败必须留痕，否则入侵检测无抓手。
		if !s.createLimit.Allow(rateLimitKey(s.clientIP(r))) {
			s.writeRateLimited(w, r, s.createLimit)
			return
		}
		if !s.authorizedAdmin(r) {
			audit.Event("admin_auth_failed", map[string]any{"remote": s.clientIP(r), "path": pathTemplate(r.URL.Path)})
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
	provided := r.Header.Get("X-Room-Admin-Token")
	if provided == "" {
		return false
	}
	// 先哈希再做常数时间比较：两侧长度恒为 32 字节，消除
	// "长度预检提前返回"泄露 token 长度的时序侧信道（与 owner token 同模式）。
	providedSum := sha256.Sum256([]byte(provided))
	wantedSum := sha256.Sum256([]byte(s.cfg.AdminToken))
	return subtle.ConstantTimeCompare(providedSum[:], wantedSum[:]) == 1
}

// writeRateLimited 写 429 并携带 Retry-After（窗口剩余秒数）。
// 客户端 roomapi 实现了 Retry-After 钳制，服务端必须提供该头，
// 否则瞬时限流下客户端按固定节奏重试、持续撞窗口边界形成自维持 429。
func (s *Server) writeRateLimited(w http.ResponseWriter, r *http.Request, l *limiter) {
	w.Header().Set("Retry-After", strconv.Itoa(l.RetryAfter(rateLimitKey(s.clientIP(r)))))
	writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
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

// clientIP 返回用于限流计数与审计的客户端地址。
// 仅当配置了可信反向代理时信任 X-Forwarded-For；否则回退到 TCP 对端地址，
// 防止公网直连下攻击者伪造头部绕过基于 IP 的限流。
//
// 安全红线：部署模型是"恰好一跳可信代理"（traefik）。traefik 会把真实
// 对端 IP 追加到 XFF 末尾并保留客户端自带的首值，因此必须取最右值——
// 取首值等于取攻击者可任意伪造的值，per-IP 限流会被完全绕过。
func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if header := r.Header.Get("X-Forwarded-For"); header != "" {
			parts := strings.Split(header, ",")
			if forwarded := strings.TrimSpace(parts[len(parts)-1]); forwarded != "" {
				return forwarded
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// rateLimitKey 把客户端 IP 聚合为限流计数键：IPv6 取 /64 前缀、IPv4 取
// /24 前缀。按完整 IP 计数时，IPv6 攻击者在 /64 内无限轮换地址即可
// 绕过全部 per-IP 限流（单 IPv6 用户通常拿到 /64 而非单地址，按完整
// IP 计数对正常用户也只是无意义的稀释）。审计日志仍记录完整 IP。
func rateLimitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		// 解析失败（代理伪头残留等）：原样作为键，同值共享配额。
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

// pathTemplate 把请求路径归一化为路由模板（如 /rooms/:code/peers）。
// 安全红线：/rooms/{code}/... 路径段是明文房间码（即入会凭证），
// 绝不允许经审计日志落盘——任何能读日志的人即可 Join 任意活跃房间。
func pathTemplate(path string) string {
	switch path {
	case "/healthz", "/rooms/healthz", "/rooms", "/rooms/join":
		return path
	}
	if strings.HasPrefix(path, "/rooms/") {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) == 3 && parts[1] != "" {
			return "/rooms/:code/" + parts[2]
		}
		return "/rooms/*"
	}
	return "other"
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type limiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	clients     map[string]counter
	lastSweep   time.Time
	sweepInterval time.Duration
}

type counter struct {
	started time.Time
	count   int
}

// maxLimiterClients clients map 的容量上限：XFF 伪造/IPv6 前缀轮换可在
// 两次清扫之间注入海量条目造成内存 DoS。达到上限时先触发全量清扫，
// 仍满则拒绝为新客户端开桶（视同限流）。
const maxLimiterClients = 100_000

func newLimiter(limit int) *limiter {
	if limit < 1 {
		limit = 1
	}
	return &limiter{limit: limit, window: time.Minute, clients: make(map[string]counter), sweepInterval: time.Minute}
}

func (l *limiter) Allow(client string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= l.sweepInterval {
		l.sweep(now)
		l.lastSweep = now
	}
	entry := l.clients[client]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		if len(l.clients) >= maxLimiterClients {
			l.sweep(now)
			if len(l.clients) >= maxLimiterClients {
				return false
			}
		}
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

// RetryAfter 返回该客户端距窗口重置的秒数（供 429 的 Retry-After 头）。
func (l *limiter) RetryAfter(client string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.clients[client]
	if entry.started.IsZero() {
		return 1
	}
	remaining := l.window - time.Since(entry.started)
	if remaining < time.Second {
		return 1
	}
	return int(remaining.Seconds())
}

func (l *limiter) sweep(now time.Time) {
	for key, entry := range l.clients {
		if now.Sub(entry.started) >= l.window {
			delete(l.clients, key)
		}
	}
}