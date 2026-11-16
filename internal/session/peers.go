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

package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"sogame/internal/roomapi"
)

type PeerAPI interface {
	Peers(context.Context, string) (roomapi.PeerList, error)
}

type PeerRefreshSnapshot struct {
	Peers         []roomapi.Peer
	Stale         bool
	LastRefreshAt time.Time
	LastError     error
}

type PeerRefresher struct {
	api   PeerAPI
	codes RoomCodeStorage
	mu    sync.Mutex
	state PeerRefreshSnapshot
	now   func() time.Time
	// rateLimitUntil 429 限流的退避截止时间：限流期间 Refresh 直接返回
	// 上次快照（stale），不向服务端发请求——避免每 5s 轮询叠加 do() 内
	// 重试把短暂限流滚成持续限流（自维持 429）。
	rateLimitUntil time.Time
}

func NewPeerRefresher(api PeerAPI, codes RoomCodeStorage) *PeerRefresher {
	return &PeerRefresher{
		api:   api,
		codes: codes,
		now:   time.Now,
		state: PeerRefreshSnapshot{Peers: []roomapi.Peer{}},
	}
}

// rateLimitFloor 429 的最小退避时长（服务端限流窗口为 1 分钟，
// 服务端 Retry-After 缺失/过小时按此退避）。
const rateLimitFloor = 15 * time.Second

func (r *PeerRefresher) Snapshot() PeerRefreshSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return clonePeerSnapshot(r.state)
}

func (r *PeerRefresher) Refresh(ctx context.Context) (PeerRefreshSnapshot, error) {
	r.mu.Lock()
	if r.now().Before(r.rateLimitUntil) {
		// 限流退避中：直接返回上次快照（stale），不再向服务端加压。
		snapshot := clonePeerSnapshot(r.state)
		snapshot.Stale = true
		r.mu.Unlock()
		return snapshot, r.state.LastError
	}
	r.mu.Unlock()

	roomCode, err := r.codes.Load()
	if err != nil {
		return r.markStale(err), err
	}
	defer clearBytes(roomCode)
	peerList, err := r.api.Peers(ctx, string(roomCode))
	if err != nil {
		var httpErr *roomapi.HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == 429 {
			backoff := httpErr.RetryAfter
			if backoff < rateLimitFloor {
				backoff = rateLimitFloor
			}
			r.mu.Lock()
			r.rateLimitUntil = r.now().Add(backoff)
			r.mu.Unlock()
		}
		return r.markStale(err), err
	}
	now := r.now().UTC()
	r.mu.Lock()
	r.state = PeerRefreshSnapshot{
		Peers:         append([]roomapi.Peer(nil), peerList.Peers...),
		LastRefreshAt: now,
	}
	r.rateLimitUntil = time.Time{}
	snapshot := clonePeerSnapshot(r.state)
	r.mu.Unlock()
	return snapshot, nil
}

func (r *PeerRefresher) markStale(err error) PeerRefreshSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Stale = true
	r.state.LastError = err
	return clonePeerSnapshot(r.state)
}

func clonePeerSnapshot(source PeerRefreshSnapshot) PeerRefreshSnapshot {
	clone := source
	clone.Peers = append([]roomapi.Peer(nil), source.Peers...)
	return clone
}
