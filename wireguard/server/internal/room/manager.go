package room

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"sogame/wireguard/server/internal/db"
	"sogame/wireguard/server/internal/ipam"
	"sogame/wireguard/server/internal/models"
	"sogame/wireguard/server/internal/ws"
)

// Manager 管理房间的创建、加入、离开
type Manager struct {
	mu   sync.Mutex // 保护房间操作原子性，防止 IPAM 竞态
	db   *db.Database
	ipam *ipam.IPAM
	hub  *ws.Hub
}

// New 创建房间管理器
func New(database *db.Database, ip *ipam.IPAM, hub *ws.Hub) *Manager {
	return &Manager{db: database, ipam: ip, hub: hub}
}

// Create 创建新房间，房主自动成为第一个节点
func (m *Manager) Create(req models.CreateRoomRequest) (*models.CreateRoomResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.Nickname == "" {
		return nil, fmt.Errorf("nickname is required")
	}
	if req.PublicKey == "" {
		return nil, fmt.Errorf("public_key is required")
	}

	// 检查公钥是否已存在于其他房间，如果是则先移除
	if existing, err := m.db.GetPeerByPublicKey(req.PublicKey); err == nil {
		oldRoomID := existing.RoomID
		_ = m.db.DeletePeer(existing.ID)
		m.hub.BroadcastToRoom(oldRoomID, models.WSMessage{
			Type: "peer_leave",
			Data: models.PeerInfo{PublicKey: existing.PublicKey, VirtualIP: existing.VirtualIP},
		})
		// 如果旧房间变空则删除
		oldPeers, _ := m.db.GetPeersByRoom(oldRoomID)
		if len(oldPeers) == 0 {
			_ = m.db.DeleteRoom(oldRoomID)
			m.hub.BroadcastToRoom(oldRoomID, models.WSMessage{
				Type: "room_deleted",
				Data: map[string]string{"room_id": oldRoomID},
			})
		}
	}

	// 分配子网
	subnet, err := m.ipam.AllocateRoomSubnet()
	if err != nil {
		return nil, fmt.Errorf("allocate subnet: %w", err)
	}

	// 创建房间
	room := &models.Room{
		ID:          uuid.NewString(),
		InviteCode:  generateInviteCode(),
		NetworkType: models.NetworkTypeWireGuard,
		Subnet:      subnet,
		CreatedAt:   time.Now(),
	}
	if err := m.db.CreateRoom(room); err != nil {
		return nil, fmt.Errorf("create room: %w", err)
	}

	// 为房主分配 IP
	virtualIP, err := m.ipam.AllocatePeerIP(room.ID, subnet)
	if err != nil {
		_ = m.db.DeleteRoom(room.ID)
		return nil, fmt.Errorf("allocate peer ip: %w", err)
	}

	// 创建房主节点
	peer := &models.Peer{
		ID:        uuid.NewString(),
		RoomID:    room.ID,
		Nickname:  req.Nickname,
		PublicKey: req.PublicKey,
		VirtualIP: virtualIP,
		Endpoint:  req.Endpoint,
		LastSeen:  time.Now(),
	}
	if err := m.db.CreatePeer(peer); err != nil {
		_ = m.db.DeleteRoom(room.ID)
		return nil, fmt.Errorf("create peer: %w", err)
	}

	return &models.CreateRoomResponse{
		RoomID:     room.ID,
		InviteCode: room.InviteCode,
		VirtualIP:  virtualIP,
		Subnet:     subnet,
	}, nil
}

// Join 通过邀请码加入房间
func (m *Manager) Join(req models.JoinRoomRequest) (*models.JoinRoomResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req.InviteCode == "" {
		return nil, fmt.Errorf("invite_code is required")
	}
	if req.Nickname == "" {
		return nil, fmt.Errorf("nickname is required")
	}
	if req.PublicKey == "" {
		return nil, fmt.Errorf("public_key is required")
	}

	// 查找房间
	r, err := m.db.GetRoomByInviteCode(req.InviteCode)
	if err != nil {
		return nil, fmt.Errorf("room not found: %w", err)
	}

	// 检查是否已加入（公钥已存在）
	existing, err := m.db.GetPeerByPublicKey(req.PublicKey)
	if err == nil {
		if existing.RoomID == r.ID {
			// 已在房间中，返回当前信息
			peers, _ := m.db.GetPeersByRoom(r.ID)
			return &models.JoinRoomResponse{
				RoomID:    r.ID,
				VirtualIP: existing.VirtualIP,
				Subnet:    r.Subnet,
				Peers:     toPeerInfos(peers, req.PublicKey),
			}, nil
		}
		// 在其他房间中，先移除
		_ = m.db.DeletePeer(existing.ID)
		m.hub.BroadcastToRoom(existing.RoomID, models.WSMessage{
			Type: "peer_leave",
			Data: models.PeerInfo{PublicKey: existing.PublicKey, VirtualIP: existing.VirtualIP},
		})
	}

	// 分配 IP
	virtualIP, err := m.ipam.AllocatePeerIP(r.ID, r.Subnet)
	if err != nil {
		return nil, fmt.Errorf("allocate peer ip: %w", err)
	}

	// 创建节点
	peer := &models.Peer{
		ID:        uuid.NewString(),
		RoomID:    r.ID,
		Nickname:  req.Nickname,
		PublicKey: req.PublicKey,
		VirtualIP: virtualIP,
		Endpoint:  req.Endpoint,
		LastSeen:  time.Now(),
	}
	if err := m.db.CreatePeer(peer); err != nil {
		return nil, fmt.Errorf("create peer: %w", err)
	}

	// 获取房间内所有节点
	peers, err := m.db.GetPeersByRoom(r.ID)
	if err != nil {
		return nil, fmt.Errorf("get peers: %w", err)
	}

	// 通知房间内其他节点有新节点加入
	m.hub.BroadcastToRoom(r.ID, models.WSMessage{
		Type: "peer_join",
		Data: toPeerInfo(peer),
	})

	return &models.JoinRoomResponse{
		RoomID:    r.ID,
		VirtualIP: virtualIP,
		Subnet:    r.Subnet,
		Peers:     toPeerInfos(peers, req.PublicKey),
	}, nil
}

// Leave 离开房间
func (m *Manager) Leave(req models.LeaveRoomRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	peer, err := m.db.GetPeerByPublicKey(req.PublicKey)
	if err != nil {
		return fmt.Errorf("peer not found: %w", err)
	}

	roomID := peer.RoomID
	if err := m.db.DeletePeer(peer.ID); err != nil {
		return fmt.Errorf("delete peer: %w", err)
	}

	// 通知房间内其他节点
	m.hub.BroadcastToRoom(roomID, models.WSMessage{
		Type: "peer_leave",
		Data: models.PeerInfo{
			PublicKey: peer.PublicKey,
			VirtualIP: peer.VirtualIP,
		},
	})

	// 检查房间是否为空，空则删除并广播
	peers, err := m.db.GetPeersByRoom(roomID)
	if err == nil && len(peers) == 0 {
		_ = m.db.DeleteRoom(roomID)
		m.hub.BroadcastToRoom(roomID, models.WSMessage{
			Type: "room_deleted",
			Data: map[string]string{"room_id": roomID},
		})
	}

	return nil
}

// GetPeers 获取房间内所有节点
func (m *Manager) GetPeers(roomID string) ([]models.PeerInfo, error) {
	peers, err := m.db.GetPeersByRoom(roomID)
	if err != nil {
		return nil, err
	}
	return toPeerInfos(peers, ""), nil
}

// Ping 更新节点心跳
func (m *Manager) Ping(pubKey, endpoint string) error {
	return m.db.UpdatePeerLastSeen(pubKey, endpoint)
}

// DeleteRoom 删除房间（管理员）
func (m *Manager) DeleteRoom(roomID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.db.DeleteRoom(roomID); err != nil {
		return err
	}
	m.hub.BroadcastToRoom(roomID, models.WSMessage{
		Type: "room_deleted",
		Data: map[string]string{"room_id": roomID},
	})
	return nil
}

// KickPeer 踢出节点（管理员）
func (m *Manager) KickPeer(peerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	peer, err := m.db.GetPeerByID(peerID)
	if err != nil {
		return fmt.Errorf("peer not found: %w", err)
	}

	roomID := peer.RoomID
	if err := m.db.DeletePeer(peer.ID); err != nil {
		return fmt.Errorf("delete peer: %w", err)
	}

	m.hub.BroadcastToRoom(roomID, models.WSMessage{
		Type: "peer_leave",
		Data: models.PeerInfo{PublicKey: peer.PublicKey, VirtualIP: peer.VirtualIP},
	})

	// 检查房间是否为空
	peers, err := m.db.GetPeersByRoom(roomID)
	if err == nil && len(peers) == 0 {
		_ = m.db.DeleteRoom(roomID)
		m.hub.BroadcastToRoom(roomID, models.WSMessage{
			Type: "room_deleted",
			Data: map[string]string{"room_id": roomID},
		})
	}

	return nil
}

// toPeerInfo 将 Peer 转为 PeerInfo
func toPeerInfo(p *models.Peer) models.PeerInfo {
	return models.PeerInfo{
		PublicKey: p.PublicKey,
		VirtualIP: p.VirtualIP,
		Endpoint:  p.Endpoint,
		Nickname:  p.Nickname,
		Online:    true,
	}
}

// toPeerInfos 将 Peer 列表转为 PeerInfo 列表
func toPeerInfos(peers []models.Peer, excludePubKey string) []models.PeerInfo {
	result := make([]models.PeerInfo, 0, len(peers))
	for _, p := range peers {
		if p.PublicKey == excludePubKey {
			continue
		}
		result = append(result, toPeerInfo(&p))
	}
	return result
}

// generateInviteCode 生成 8 位邀请码
func generateInviteCode() string {
	return uuid.NewString()[:8]
}
