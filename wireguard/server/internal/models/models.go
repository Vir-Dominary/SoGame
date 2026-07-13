package models

import "time"

// NetworkType 标识房间使用的组网模式
type NetworkType string

const (
	NetworkTypeWireGuard NetworkType = "wireguard"
	NetworkTypeN2N       NetworkType = "n2n"
)

// Room 表示一个虚拟组网房间
type Room struct {
	ID          string      `json:"id"`
	InviteCode  string      `json:"invite_code"`
	NetworkType NetworkType `json:"network_type"`
	Subnet      string      `json:"subnet"`
	CreatedAt   time.Time   `json:"created_at"`
}

// Peer 表示房间中的一个对等节点
type Peer struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"room_id"`
	Nickname  string    `json:"nickname"`
	PublicKey string    `json:"public_key"`
	VirtualIP string    `json:"virtual_ip"`
	Endpoint  string    `json:"endpoint"`
	LastSeen  time.Time `json:"last_seen"`
	Online    bool      `json:"online"`
}

// CreateRoomRequest 创建房间请求
type CreateRoomRequest struct {
	Nickname  string `json:"nickname"`
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
}

// CreateRoomResponse 创建房间响应
type CreateRoomResponse struct {
	RoomID     string `json:"room_id"`
	InviteCode string `json:"invite_code"`
	VirtualIP  string `json:"virtual_ip"`
	Subnet     string `json:"subnet"`
}

// JoinRoomRequest 加入房间请求
type JoinRoomRequest struct {
	InviteCode string `json:"invite_code"`
	Nickname   string `json:"nickname"`
	PublicKey  string `json:"public_key"`
	Endpoint   string `json:"endpoint"`
}

// JoinRoomResponse 加入房间响应
type JoinRoomResponse struct {
	RoomID    string     `json:"room_id"`
	VirtualIP string     `json:"virtual_ip"`
	Subnet    string     `json:"subnet"`
	Peers     []PeerInfo `json:"peers"`
}

// LeaveRoomRequest 离开房间请求
type LeaveRoomRequest struct {
	PublicKey string `json:"public_key"`
}

// PeerInfo 返回给客户端的节点信息（不含敏感字段）
type PeerInfo struct {
	PublicKey string `json:"public_key"`
	VirtualIP string `json:"virtual_ip"`
	Endpoint  string `json:"endpoint"`
	Nickname  string `json:"nickname"`
	Online    bool   `json:"online"`
}

// PingRequest 心跳请求
type PingRequest struct {
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
}

// PingResponse 心跳响应
type PingResponse struct {
	OK bool `json:"ok"`
}

// WSMessage WebSocket 消息
type WSMessage struct {
	Type string      `json:"type"` // peer_join, peer_leave, peer_update
	Data interface{} `json:"data"`
}

// AdminStats 后台统计
type AdminStats struct {
	OnlineUsers int `json:"online_users"`
	OnlineRooms int `json:"online_rooms"`
	TotalRooms  int `json:"total_rooms"`
	TotalPeers  int `json:"total_peers"`
}
