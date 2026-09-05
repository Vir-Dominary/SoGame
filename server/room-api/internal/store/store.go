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

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("not found")

type Room struct {
	ID                 string
	CodeHash           []byte
	CodeCiphertext     []byte
	GroupID            string
	SetupKeyID         string
	SetupKeyCiphertext []byte
	PolicyID           string
	Status             string
	CreatedAt          time.Time
	DisabledAt         *time.Time
	LastError          string
	// 房主机制（房间回收）:
	// OwnerTokenHash 是创建者令牌的 SHA256(hex),空串表示老房间(无房主能力)。
	OwnerTokenHash     string
	LastOwnerHeartbeat *time.Time
	ClosedAt           *time.Time
	ClosedReason       string
}

type Operation struct {
	IdempotencyKey string
	RoomID         string
	Response       []byte
	Status         string
}

type RoomOperation struct {
	RoomID string
	Operation
}

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS rooms (
  id TEXT PRIMARY KEY,
  code_hash BLOB NOT NULL UNIQUE,
  code_ciphertext BLOB NOT NULL,
  group_id TEXT NOT NULL DEFAULT '',
  setup_key_id TEXT NOT NULL DEFAULT '',
  setup_key_ciphertext BLOB NOT NULL DEFAULT '',
  policy_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  disabled_at INTEGER,
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_rooms_status ON rooms(status);
CREATE TABLE IF NOT EXISTS operations (
  idempotency_key TEXT PRIMARY KEY,
  room_id TEXT NOT NULL,
  response_ciphertext BLOB NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
`)
	if err != nil {
		return err
	}
	// 房主机制列:SQLite 不支持 IF NOT EXISTS 的 ADD COLUMN,先查再加。
	for _, column := range []struct{ name, ddl string }{
		{"owner_token_hash", "ALTER TABLE rooms ADD COLUMN owner_token_hash TEXT NOT NULL DEFAULT ''"},
		{"last_owner_heartbeat", "ALTER TABLE rooms ADD COLUMN last_owner_heartbeat INTEGER"},
		{"closed_at", "ALTER TABLE rooms ADD COLUMN closed_at INTEGER"},
		{"closed_reason", "ALTER TABLE rooms ADD COLUMN closed_reason TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureRoomColumn(ctx, column.name, column.ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureRoomColumn(ctx context.Context, name, ddl string) error {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(rooms)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, ddl)
	return err
}

func (s *Store) BeginOperation(ctx context.Context, key, roomID string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO operations(idempotency_key, room_id, status, created_at) VALUES(?, ?, 'creating', ?)`, key, roomID, time.Now().UnixNano())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) GetOperation(ctx context.Context, key string) (Operation, error) {
	var op Operation
	err := s.DB.QueryRowContext(ctx, `SELECT idempotency_key, room_id, response_ciphertext, status FROM operations WHERE idempotency_key=?`, key).Scan(&op.IdempotencyKey, &op.RoomID, &op.Response, &op.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrNotFound
	}
	return op, err
}

func (s *Store) SaveOperation(ctx context.Context, key string, response []byte, status string) error {
	if response == nil {
		response = []byte{}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE operations SET response_ciphertext=?, status=? WHERE idempotency_key=?`, response, status, key)
	return err
}

func (s *Store) ResetOperation(ctx context.Context, key, roomID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE operations SET room_id=?, response_ciphertext='', status='creating' WHERE idempotency_key=?`, roomID, key)
	return err
}

func (s *Store) CreateRoom(ctx context.Context, room Room) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO rooms(id, code_hash, code_ciphertext, status, created_at) VALUES(?, ?, ?, ?, ?)`, room.ID, room.CodeHash, room.CodeCiphertext, room.Status, room.CreatedAt.UnixNano())
	return err
}

func (s *Store) UpdateExternalIDs(ctx context.Context, roomID, groupID, setupKeyID, policyID string, setupKeyCiphertext []byte) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET group_id=?, setup_key_id=?, setup_key_ciphertext=?, policy_id=? WHERE id=?`, groupID, setupKeyID, setupKeyCiphertext, policyID, roomID)
	return err
}

func (s *Store) SetStatus(ctx context.Context, roomID, status, lastError string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET status=?, last_error=? WHERE id=?`, status, lastError, roomID)
	return err
}

func (s *Store) Disable(ctx context.Context, roomID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET status='disabled', disabled_at=?, last_error='' WHERE id=?`, time.Now().UnixNano(), roomID)
	return err
}

func (s *Store) GetRoomByCodeHash(ctx context.Context, hash []byte) (Room, error) {
	return s.scanRoom(s.DB.QueryRowContext(ctx, `SELECT id, code_hash, code_ciphertext, group_id, setup_key_id, setup_key_ciphertext, policy_id, status, created_at, disabled_at, last_error, owner_token_hash, last_owner_heartbeat, closed_at, closed_reason FROM rooms WHERE code_hash=?`, hash))
}

func (s *Store) GetRoom(ctx context.Context, id string) (Room, error) {
	return s.scanRoom(s.DB.QueryRowContext(ctx, `SELECT id, code_hash, code_ciphertext, group_id, setup_key_id, setup_key_ciphertext, policy_id, status, created_at, disabled_at, last_error, owner_token_hash, last_owner_heartbeat, closed_at, closed_reason FROM rooms WHERE id=?`, id))
}

// SetOwnerTokenHash 记录房主令牌哈希（只在创建阶段调用一次）。
func (s *Store) SetOwnerTokenHash(ctx context.Context, roomID, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET owner_token_hash=? WHERE id=?`, tokenHash, roomID)
	return err
}

// TouchOwnerHeartbeat 记录房主心跳时间。
func (s *Store) TouchOwnerHeartbeat(ctx context.Context, roomID string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET last_owner_heartbeat=? WHERE id=?`, at.UnixNano(), roomID)
	return err
}

// MarkRoomClosed 将房间标记为已关闭（含原因与时刻）。
func (s *Store) MarkRoomClosed(ctx context.Context, roomID, reason string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE rooms SET status='closed', closed_reason=?, closed_at=? WHERE id=?`, reason, at.UnixNano(), roomID)
	return err
}

// ListOwnerSweepCandidates 返回需要被清扫的 active 房间：
// 有房主令牌（新机制房间）且房主心跳已过期（或从未心跳且创建超过 cutoff）。
func (s *Store) ListOwnerSweepCandidates(ctx context.Context, cutoff time.Time) ([]Room, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, code_hash, code_ciphertext, group_id, setup_key_id, setup_key_ciphertext, policy_id, status, created_at, disabled_at, last_error, owner_token_hash, last_owner_heartbeat, closed_at, closed_reason
FROM rooms
WHERE status='active' AND owner_token_hash <> ''
  AND ((last_owner_heartbeat IS NULL AND created_at <= ?) OR (last_owner_heartbeat IS NOT NULL AND last_owner_heartbeat <= ?))`,
		cutoff.UnixNano(), cutoff.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		room, err := s.scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *Store) ListRoomsByStatus(ctx context.Context, status string) ([]Room, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, code_hash, code_ciphertext, group_id, setup_key_id, setup_key_ciphertext, policy_id, status, created_at, disabled_at, last_error, owner_token_hash, last_owner_heartbeat, closed_at, closed_reason FROM rooms WHERE status=?`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []Room
	for rows.Next() {
		room, err := s.scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *Store) ListOperationsByStatus(ctx context.Context, status string) ([]RoomOperation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT idempotency_key, room_id, response_ciphertext, status FROM operations WHERE status=?`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var operations []RoomOperation
	for rows.Next() {
		var operation RoomOperation
		if err := rows.Scan(&operation.IdempotencyKey, &operation.RoomID, &operation.Response, &operation.Status); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *Store) scanRoom(row rowScanner) (Room, error) {
	var room Room
	var created int64
	var disabled, heartbeat, closed sql.NullInt64
	err := row.Scan(&room.ID, &room.CodeHash, &room.CodeCiphertext, &room.GroupID, &room.SetupKeyID, &room.SetupKeyCiphertext, &room.PolicyID, &room.Status, &created, &disabled, &room.LastError, &room.OwnerTokenHash, &heartbeat, &closed, &room.ClosedReason)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, err
	}
	room.CreatedAt = time.Unix(0, created).UTC()
	if disabled.Valid {
		t := time.Unix(0, disabled.Int64).UTC()
		room.DisabledAt = &t
	}
	if heartbeat.Valid {
		t := time.Unix(0, heartbeat.Int64).UTC()
		room.LastOwnerHeartbeat = &t
	}
	if closed.Valid {
		t := time.Unix(0, closed.Int64).UTC()
		room.ClosedAt = &t
	}
	return room, nil
}

func (s *Store) MustHealthy(ctx context.Context) error {
	var result string
	if err := s.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err == nil && result == "ok" {
		return nil
	}
	return fmt.Errorf("store health check failed")
}