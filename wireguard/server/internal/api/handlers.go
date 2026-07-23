package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"sogame/wireguard/server/internal/db"
	"sogame/wireguard/server/internal/models"
	"sogame/wireguard/server/internal/room"
)

// Handler 处理所有 REST API 请求
type Handler struct {
	room       *room.Manager
	db         *db.Database
	adminToken string // 为空时 admin 端点完全禁用（返回 403）
}

// New 创建 API Handler。
// adminToken 为空时，所有 /api/admin/* 端点将返回 403 Forbidden。
func New(roomMgr *room.Manager, database *db.Database, adminToken string) *Handler {
	return &Handler{room: roomMgr, db: database, adminToken: adminToken}
}

// RegisterRoutes 注册所有 API 路由。
// /api/admin/* 路由通过 adminAuthMiddleware 包裹，需要 Bearer Token 认证。
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// 公共 API（无需认证）
	mux.HandleFunc("/api/room/create", h.handleCreateRoom)
	mux.HandleFunc("/api/room/join", h.handleJoinRoom)
	mux.HandleFunc("/api/room/leave", h.handleLeaveRoom)
	mux.HandleFunc("/api/room/peers", h.handleGetPeers)
	mux.HandleFunc("/api/ping", h.handlePing)

	// Admin API（需要 Bearer Token 认证）
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/api/admin/stats", h.handleAdminStats)
	adminMux.HandleFunc("/api/admin/rooms", h.handleAdminRooms)
	adminMux.HandleFunc("/api/admin/peers", h.handleAdminPeers)
	adminMux.HandleFunc("/api/admin/room/", h.handleAdminRoomAction)
	adminMux.HandleFunc("/api/admin/peer/", h.handleAdminPeerAction)
	mux.Handle("/api/admin/", h.adminAuthMiddleware(adminMux))
}

// adminAuthMiddleware 校验 Authorization: Bearer <token> 头。
// 若 adminToken 为空，admin 端点完全禁用（返回 403）；
// 若 token 不匹配，返回 401。
func (h *Handler) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.adminToken == "" {
			writeError(w, http.StatusForbidden, "admin API is disabled (no token configured)")
			return
		}

		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
			writeError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := auth[len(prefix):]
		if token != h.adminToken {
			writeError(w, http.StatusUnauthorized, "invalid admin token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *Handler) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.CreateRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.room.Create(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.JoinRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.room.Join(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleLeaveRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.LeaveRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.room.Leave(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) handleGetPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		writeError(w, http.StatusBadRequest, "room_id is required")
		return
	}

	peers, err := h.room.GetPeers(roomID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, peers)
}

func (h *Handler) handlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.PingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.room.Ping(req.PublicKey, req.Endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, models.PingResponse{OK: true})
}

// --- Admin ---

func (h *Handler) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	onlinePeers, _ := h.db.CountOnlinePeers()
	onlineRooms, _ := h.db.CountOnlineRooms()
	totalRooms, _ := h.db.CountRooms()
	totalPeers, _ := h.db.CountPeers()

	writeJSON(w, http.StatusOK, models.AdminStats{
		OnlineUsers: onlinePeers,
		OnlineRooms: onlineRooms,
		TotalRooms:  totalRooms,
		TotalPeers:  totalPeers,
	})
}

func (h *Handler) handleAdminRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rooms, err := h.db.ListRooms()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rooms)
}

func (h *Handler) handleAdminPeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	roomID := r.URL.Query().Get("room_id")
	var peers []models.Peer
	var err error

	if roomID != "" {
		peers, err = h.db.GetPeersByRoom(roomID)
	} else {
		// 返回所有房间的所有节点
		rooms, roomErr := h.db.ListRooms()
		if roomErr != nil {
			writeError(w, http.StatusInternalServerError, roomErr.Error())
			return
		}
		for _, room := range rooms {
			roomPeers, pErr := h.db.GetPeersByRoom(room.ID)
			if pErr != nil {
				continue
			}
			peers = append(peers, roomPeers...)
		}
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, peers)
}

func (h *Handler) handleAdminRoomAction(w http.ResponseWriter, r *http.Request) {
	// /api/admin/room/{room_id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/room/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "room_id is required")
		return
	}

	roomID := parts[0]
	if r.Method == http.MethodDelete {
		if err := h.room.DeleteRoom(roomID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *Handler) handleAdminPeerAction(w http.ResponseWriter, r *http.Request) {
	// /api/admin/peer/{peer_id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/peer/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	peerID := parts[0]
	if r.Method == http.MethodDelete {
		if err := h.room.KickPeer(peerID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("api: write json error: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
