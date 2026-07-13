package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"sogame/wireguard/agent/internal/logger"
	"sogame/wireguard/agent/internal/models"
	"sogame/wireguard/agent/internal/wg"
)

// Listener 监听控制服务器的 WebSocket 通知，动态增删 WireGuard 节点
type Listener struct {
	mu            sync.Mutex
	conn          *websocket.Conn
	wgMgr         *wg.Manager
	wsURL         string
	done          chan struct{}
	closed        bool
	OnRoomDeleted func() // 房间被删除时的回调
}

// New 创建 WebSocket 监听器
func New(wgMgr *wg.Manager) *Listener {
	return &Listener{
		wgMgr: wgMgr,
	}
}

// Connect 连接到控制服务器的 WebSocket
func (l *Listener) Connect(wsURL string) error {
	header := http.Header{}
	header.Set("Origin", "http://localhost")

	// 在锁外执行网络拨号，避免持锁阻塞
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}

	l.mu.Lock()
	l.wsURL = wsURL
	l.done = make(chan struct{})
	l.conn = conn
	l.closed = false
	l.mu.Unlock()

	logger.Infof("websocket connected: %s", wsURL)

	go l.readLoop()
	go l.pingLoop()

	return nil
}

// Disconnect 断开 WebSocket 连接
func (l *Listener) Disconnect() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	if l.done != nil {
		close(l.done)
		l.done = nil
	}
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
}

// readLoop 读取 WebSocket 消息
func (l *Listener) readLoop() {
	for {
		l.mu.Lock()
		if l.closed || l.conn == nil {
			l.mu.Unlock()
			return
		}
		conn := l.conn
		l.mu.Unlock()

		_, message, err := conn.ReadMessage()
		if err != nil {
			logger.Warnf("websocket read error: %v", err)
			l.Disconnect()
			return
		}

		var msg models.WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			logger.Warnf("unmarshal ws message: %v", err)
			continue
		}

		l.handleMessage(&msg)
	}
}

// handleMessage 处理 WebSocket 消息
func (l *Listener) handleMessage(msg *models.WSMessage) {
	switch msg.Type {
	case "peer_join":
		var data models.PeerEventData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			logger.Warnf("unmarshal peer_join: %v", err)
			return
		}
		peer := &wg.Peer{
			PublicKey: data.PublicKey,
			VirtualIP: data.VirtualIP,
			Endpoint:  data.Endpoint,
		}
		if err := l.wgMgr.AddPeer(peer); err != nil {
			logger.Errorf("add peer on join: %v", err)
		}

	case "peer_leave":
		var data models.PeerEventData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			logger.Warnf("unmarshal peer_leave: %v", err)
			return
		}
		if err := l.wgMgr.RemovePeer(data.PublicKey); err != nil {
			logger.Errorf("remove peer on leave: %v", err)
		}

	case "peer_update":
		var data models.PeerEventData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		peer := &wg.Peer{
			PublicKey: data.PublicKey,
			VirtualIP: data.VirtualIP,
			Endpoint:  data.Endpoint,
		}
		_ = l.wgMgr.AddPeer(peer)

	case "room_deleted":
		logger.Infof("room deleted, disconnecting")
		_ = l.wgMgr.Disconnect()
		if l.OnRoomDeleted != nil {
			l.OnRoomDeleted()
		}

	default:
		logger.Infof("unknown ws message type: %s", msg.Type)
	}
}

// pingLoop 定期发送 ping 保活
func (l *Listener) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		l.mu.Lock()
		done := l.done
		l.mu.Unlock()

		if done == nil {
			return
		}

		select {
		case <-done:
			return
		case <-ticker.C:
			l.mu.Lock()
			conn := l.conn
			l.mu.Unlock()
			if conn == nil {
				return
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				logger.Warnf("websocket ping: %v", err)
				return
			}
		}
	}
}
