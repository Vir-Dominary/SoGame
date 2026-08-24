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
	"sync"
	"time"

	"sogame/internal/roomapi"
)

const (
	ForegroundPeerRefreshInterval = 5 * time.Second
	TrayPeerRefreshInterval       = 30 * time.Second
)

type PeerAPI interface {
	Peers(context.Context, string) (roomapi.PeerList, error)
}

type PeerRefreshMode string

const (
	PeerRefreshForeground PeerRefreshMode = "foreground"
	PeerRefreshTray       PeerRefreshMode = "tray"
)

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
}

func NewPeerRefresher(api PeerAPI, codes RoomCodeStorage) *PeerRefresher {
	return &PeerRefresher{
		api:   api,
		codes: codes,
		now:   time.Now,
		state: PeerRefreshSnapshot{Peers: []roomapi.Peer{}},
	}
}

func (r *PeerRefresher) Snapshot() PeerRefreshSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return clonePeerSnapshot(r.state)
}

func (r *PeerRefresher) Refresh(ctx context.Context) (PeerRefreshSnapshot, error) {
	roomCode, err := r.codes.Load()
	if err != nil {
		return r.markStale(err), err
	}
	defer clearBytes(roomCode)
	peerList, err := r.api.Peers(ctx, string(roomCode))
	if err != nil {
		return r.markStale(err), err
	}
	now := r.now().UTC()
	r.mu.Lock()
	r.state = PeerRefreshSnapshot{
		Peers:         append([]roomapi.Peer(nil), peerList.Peers...),
		LastRefreshAt: now,
	}
	snapshot := clonePeerSnapshot(r.state)
	r.mu.Unlock()
	return snapshot, nil
}

func (r *PeerRefresher) Watch(ctx context.Context, mode PeerRefreshMode) <-chan PeerRefreshSnapshot {
	updates := make(chan PeerRefreshSnapshot, 1)
	interval := peerRefreshInterval(mode)
	go func() {
		defer close(updates)
		emit := func(snapshot PeerRefreshSnapshot) bool {
			select {
			case updates <- snapshot:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if snapshot, _ := r.Refresh(ctx); !emit(snapshot) {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snapshot, _ := r.Refresh(ctx)
				if !emit(snapshot) {
					return
				}
			}
		}
	}()
	return updates
}

func peerRefreshInterval(mode PeerRefreshMode) time.Duration {
	if mode == PeerRefreshTray {
		return TrayPeerRefreshInterval
	}
	return ForegroundPeerRefreshInterval
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
