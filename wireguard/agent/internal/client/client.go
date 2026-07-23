package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sogame/wireguard/agent/internal/models"
)

// Client 是控制服务器的 HTTP 客户端
type Client struct {
	baseURL string
	http    *http.Client
}

// New 创建控制服务器客户端。
// 自动为缺少协议前缀的 baseURL 补充 http://，否则 http.Post 会将
// "127.0.0.1:8080/api/room/create" 当作相对路径解析，触发
// "first path segment in URL cannot contain colon" 错误。
func New(baseURL string) *Client {
	return &Client{
		baseURL: normalizeBaseURL(baseURL),
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// normalizeBaseURL 确保 baseURL 包含 http:// 或 https:// 协议前缀。
func normalizeBaseURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return url
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	return "http://" + url
}

// CreateRoom 创建房间
func (c *Client) CreateRoom(req models.CreateRoomRequest) (*models.CreateRoomResponse, error) {
	var resp models.CreateRoomResponse
	if err := c.post("/api/room/create", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// JoinRoom 加入房间
func (c *Client) JoinRoom(req models.JoinRoomRequest) (*models.JoinRoomResponse, error) {
	var resp models.JoinRoomResponse
	if err := c.post("/api/room/join", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LeaveRoom 离开房间
func (c *Client) LeaveRoom(req models.LeaveRoomRequest) error {
	return c.post("/api/room/leave", req, nil)
}

// GetPeers 获取房间节点列表
func (c *Client) GetPeers(roomID string) ([]models.PeerInfo, error) {
	var resp []models.PeerInfo
	if err := c.get(fmt.Sprintf("/api/room/peers?room_id=%s", roomID), &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Ping 发送心跳
func (c *Client) Ping(req models.PingRequest) error {
	return c.post("/api/ping", req, nil)
}

// WSURL 返回 WebSocket 连接 URL。
// 自动将 http(s):// 转换为 ws(s)://，因为 gorilla/websocket 要求显式的 ws/wss 协议。
func (c *Client) WSURL(roomID string) string {
	wsScheme := "ws"
	base := c.baseURL
	switch {
	case strings.HasPrefix(base, "https://"):
		wsScheme = "wss"
		base = strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = strings.TrimPrefix(base, "http://")
	}
	return wsScheme + "://" + base + "/ws/room/" + roomID
}

// post 发送 POST 请求
func (c *Client) post(path string, body interface{}, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	resp, err := c.http.Post(c.baseURL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// get 发送 GET 请求
func (c *Client) get(path string, result interface{}) error {
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}
