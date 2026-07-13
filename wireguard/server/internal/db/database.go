package db

import (
	"database/sql"
	"fmt"
	"time"

	"sogame/wireguard/server/internal/models"

	_ "modernc.org/sqlite"
)

// Database 封装 SQLite 数据库操作
type Database struct {
	db *sql.DB
}

// New 创建并初始化数据库
func New(dbPath string) (*Database, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite 单写并发限制
	d := &Database{db: db}

	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// 验证数据库连接可用
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return d, nil
}

func (d *Database) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS room (
			id           TEXT PRIMARY KEY,
			invite_code  TEXT UNIQUE NOT NULL,
			network_type TEXT NOT NULL DEFAULT 'wireguard',
			subnet       TEXT NOT NULL,
			created_at   DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS peer (
			id          TEXT PRIMARY KEY,
			room_id     TEXT NOT NULL,
			nickname    TEXT NOT NULL,
			public_key  TEXT UNIQUE NOT NULL,
			virtual_ip  TEXT NOT NULL,
			endpoint    TEXT NOT NULL DEFAULT '',
			last_seen   DATETIME NOT NULL,
			FOREIGN KEY (room_id) REFERENCES room(id) ON DELETE CASCADE
		);

		CREATE UNIQUE INDEX IF NOT EXISTS idx_peer_room_ip ON peer(room_id, virtual_ip);
		CREATE INDEX IF NOT EXISTS idx_peer_room ON peer(room_id);
		CREATE INDEX IF NOT EXISTS idx_peer_pubkey ON peer(public_key);
	`)
	return err
}

// Close 关闭数据库
func (d *Database) Close() error {
	return d.db.Close()
}

// --- Room ---

// CreateRoom 插入新房间
func (d *Database) CreateRoom(r *models.Room) error {
	_, err := d.db.Exec(
		`INSERT INTO room (id, invite_code, network_type, subnet, created_at) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.InviteCode, string(r.NetworkType), r.Subnet, r.CreatedAt,
	)
	return err
}

// GetRoomByInviteCode 根据邀请码查询房间
func (d *Database) GetRoomByInviteCode(code string) (*models.Room, error) {
	var r models.Room
	var netType string
	err := d.db.QueryRow(
		`SELECT id, invite_code, network_type, subnet, created_at FROM room WHERE invite_code = ?`,
		code,
	).Scan(&r.ID, &r.InviteCode, &netType, &r.Subnet, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.NetworkType = models.NetworkType(netType)
	return &r, nil
}

// GetRoom 根据 ID 查询房间
func (d *Database) GetRoom(id string) (*models.Room, error) {
	var r models.Room
	var netType string
	err := d.db.QueryRow(
		`SELECT id, invite_code, network_type, subnet, created_at FROM room WHERE id = ?`,
		id,
	).Scan(&r.ID, &r.InviteCode, &netType, &r.Subnet, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	r.NetworkType = models.NetworkType(netType)
	return &r, nil
}

// DeleteRoom 删除房间
func (d *Database) DeleteRoom(id string) error {
	_, err := d.db.Exec(`DELETE FROM room WHERE id = ?`, id)
	return err
}

// ListRooms 列出所有房间
func (d *Database) ListRooms() ([]models.Room, error) {
	rows, err := d.db.Query(
		`SELECT id, invite_code, network_type, subnet, created_at FROM room ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []models.Room
	for rows.Next() {
		var r models.Room
		var netType string
		if err := rows.Scan(&r.ID, &r.InviteCode, &netType, &r.Subnet, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.NetworkType = models.NetworkType(netType)
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

// CountRooms 统计房间总数
func (d *Database) CountRooms() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM room`).Scan(&count)
	return count, err
}

// --- Peer ---

// CreatePeer 插入新节点
func (d *Database) CreatePeer(p *models.Peer) error {
	_, err := d.db.Exec(
		`INSERT INTO peer (id, room_id, nickname, public_key, virtual_ip, endpoint, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.RoomID, p.Nickname, p.PublicKey, p.VirtualIP, p.Endpoint, p.LastSeen,
	)
	return err
}

// GetPeerByPublicKey 根据公钥查询节点
func (d *Database) GetPeerByPublicKey(pubKey string) (*models.Peer, error) {
	var p models.Peer
	err := d.db.QueryRow(
		`SELECT id, room_id, nickname, public_key, virtual_ip, endpoint, last_seen FROM peer WHERE public_key = ?`,
		pubKey,
	).Scan(&p.ID, &p.RoomID, &p.Nickname, &p.PublicKey, &p.VirtualIP, &p.Endpoint, &p.LastSeen)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPeerByID 根据 ID 查询节点
func (d *Database) GetPeerByID(id string) (*models.Peer, error) {
	var p models.Peer
	err := d.db.QueryRow(
		`SELECT id, room_id, nickname, public_key, virtual_ip, endpoint, last_seen FROM peer WHERE id = ?`,
		id,
	).Scan(&p.ID, &p.RoomID, &p.Nickname, &p.PublicKey, &p.VirtualIP, &p.Endpoint, &p.LastSeen)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPeersByRoom 查询房间内所有节点
func (d *Database) GetPeersByRoom(roomID string) ([]models.Peer, error) {
	rows, err := d.db.Query(
		`SELECT id, room_id, nickname, public_key, virtual_ip, endpoint, last_seen FROM peer WHERE room_id = ?`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var peers []models.Peer
	for rows.Next() {
		var p models.Peer
		if err := rows.Scan(&p.ID, &p.RoomID, &p.Nickname, &p.PublicKey, &p.VirtualIP, &p.Endpoint, &p.LastSeen); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

// DeletePeer 删除节点
func (d *Database) DeletePeer(id string) error {
	_, err := d.db.Exec(`DELETE FROM peer WHERE id = ?`, id)
	return err
}

// DeletePeerByPublicKey 根据公钥删除节点
func (d *Database) DeletePeerByPublicKey(pubKey string) error {
	_, err := d.db.Exec(`DELETE FROM peer WHERE public_key = ?`, pubKey)
	return err
}

// UpdatePeerLastSeen 更新节点最后在线时间
func (d *Database) UpdatePeerLastSeen(pubKey, endpoint string) error {
	_, err := d.db.Exec(
		`UPDATE peer SET last_seen = ?, endpoint = ? WHERE public_key = ?`,
		time.Now(), endpoint, pubKey,
	)
	return err
}

// CountOnlinePeers 统计在线节点数（最近 60 秒内有心跳）
func (d *Database) CountOnlinePeers() (int, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM peer WHERE last_seen > ?`,
		time.Now().Add(-60*time.Second),
	).Scan(&count)
	return count, err
}

// CountOnlineRooms 统计有在线节点的房间数
func (d *Database) CountOnlineRooms() (int, error) {
	var count int
	err := d.db.QueryRow(
		`SELECT COUNT(DISTINCT room_id) FROM peer WHERE last_seen > ?`,
		time.Now().Add(-60*time.Second),
	).Scan(&count)
	return count, err
}

// CountPeers 统计节点总数
func (d *Database) CountPeers() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM peer`).Scan(&count)
	return count, err
}

// GetUsedIPsInRoom 获取房间内已分配的 IP
func (d *Database) GetUsedIPsInRoom(roomID string) ([]string, error) {
	rows, err := d.db.Query(
		`SELECT virtual_ip FROM peer WHERE room_id = ?`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		ips = append(ips, ip)
	}
	return ips, rows.Err()
}
