package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"sogame/wireguard/server/internal/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许所有来源（控制平面，非敏感数据）
	},
}

// Hub 管理所有 WebSocket 连接，按房间分组
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]bool // room_id -> clients
}

// Client 表示一个 WebSocket 客户端
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	roomID string
}

// New 创建 Hub
func New() *Hub {
	return &Hub{
		clients: make(map[string]map[*Client]bool),
	}
}

// Register 注册客户端到房间
func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.clients[client.roomID] == nil {
		h.clients[client.roomID] = make(map[*Client]bool)
	}
	h.clients[client.roomID][client] = true
}

// Unregister 注销客户端
func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if clients, ok := h.clients[client.roomID]; ok {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			close(client.send)
		}
		if len(clients) == 0 {
			delete(h.clients, client.roomID)
		}
	}
}

// BroadcastToRoom 向房间内所有客户端广播消息
func (h *Hub) BroadcastToRoom(roomID string, msg models.WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws: marshal error: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.clients[roomID]; ok {
		for client := range clients {
			select {
			case client.send <- data:
			default:
				// 客户端缓冲已满，跳过
			}
		}
	}
}

// GetUpgrader 返回 WebSocket upgrader
func GetUpgrader() websocket.Upgrader {
	return upgrader
}
