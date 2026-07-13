package models

import "encoding/json"

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

// PeerInfo 节点信息
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

// WSMessage WebSocket 消息
type WSMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// PeerEventData 节点事件数据
type PeerEventData struct {
	PublicKey string `json:"public_key"`
	VirtualIP string `json:"virtual_ip"`
	Endpoint  string `json:"endpoint"`
	Nickname  string `json:"nickname"`
}
