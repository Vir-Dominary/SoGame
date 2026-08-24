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
	"sync"

	clientnetbird "sogame/internal/netbird"
)

type State string

const (
	StateNoRoom                State = "NoRoom"
	StateEnrolling             State = "Enrolling"
	StateControlPlaneConnected State = "ControlPlaneConnected"
	StateWaitingForPeer        State = "WaitingForPeer"
	StateConnectingPeer        State = "ConnectingPeer"
	StateConnectedP2P          State = "ConnectedP2P"
	StateConnectedRelay        State = "ConnectedRelay"
	StateReconnecting          State = "Reconnecting"
	StateRecoverableError      State = "RecoverableError"
)

type Facts struct {
	RoomSaved              bool
	EnrollmentInProgress   bool
	ReconnectInProgress    bool
	UserDisconnected       bool
	RecoverableError       bool
	ControlPlaneReady      bool
	MembershipKnown        bool
	OtherRoomPeerCount     int
	PeerConnectionTimedOut bool
	DaemonPeers            []clientnetbird.Peer
}

func FactsFromDaemon(roomSaved, membershipKnown bool, otherRoomPeerCount int, snapshot clientnetbird.Snapshot) Facts {
	return Facts{
		RoomSaved:          roomSaved,
		ControlPlaneReady:  snapshot.ManagementConnected && snapshot.SignalConnected,
		MembershipKnown:    membershipKnown,
		OtherRoomPeerCount: otherRoomPeerCount,
		DaemonPeers:        append([]clientnetbird.Peer(nil), snapshot.Peers...),
	}
}

type Snapshot struct {
	Revision uint64
	State    State
	Path     clientnetbird.PathType
}

type Machine struct {
	mu       sync.RWMutex
	revision uint64
	state    State
	path     clientnetbird.PathType
}

func NewMachine() *Machine {
	return &Machine{state: StateNoRoom, path: clientnetbird.PathNone}
}

func (m *Machine) Apply(facts Facts) Snapshot {
	state, path := Derive(facts)
	m.mu.Lock()
	defer m.mu.Unlock()
	if state != m.state || path != m.path {
		m.revision++
		m.state = state
		m.path = path
	}
	return Snapshot{Revision: m.revision, State: m.state, Path: m.path}
}

func (m *Machine) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Snapshot{Revision: m.revision, State: m.state, Path: m.path}
}

func Derive(facts Facts) (State, clientnetbird.PathType) {
	if facts.RecoverableError {
		return StateRecoverableError, clientnetbird.PathNone
	}
	if facts.EnrollmentInProgress {
		return StateEnrolling, clientnetbird.PathNone
	}
	if !facts.RoomSaved {
		return StateNoRoom, clientnetbird.PathNone
	}
	if facts.ReconnectInProgress {
		return StateReconnecting, clientnetbird.PathNone
	}
	if facts.UserDisconnected {
		return StateControlPlaneConnected, clientnetbird.PathNone
	}

	path := preferredConnectedPath(facts.DaemonPeers)
	switch path {
	case clientnetbird.PathP2P:
		return StateConnectedP2P, path
	case clientnetbird.PathRelay:
		return StateConnectedRelay, path
	}
	if !facts.ControlPlaneReady {
		return StateReconnecting, clientnetbird.PathNone
	}
	if !facts.MembershipKnown {
		return StateControlPlaneConnected, clientnetbird.PathNone
	}
	if facts.OtherRoomPeerCount <= 0 {
		return StateWaitingForPeer, clientnetbird.PathNone
	}
	if facts.PeerConnectionTimedOut {
		return StateReconnecting, clientnetbird.PathNone
	}
	return StateConnectingPeer, clientnetbird.PathNone
}

func preferredConnectedPath(peers []clientnetbird.Peer) clientnetbird.PathType {
	path := clientnetbird.PathNone
	for _, peer := range peers {
		if peer.State != clientnetbird.PeerConnected {
			continue
		}
		switch peer.Path {
		case clientnetbird.PathP2P:
			return clientnetbird.PathP2P
		case clientnetbird.PathRelay:
			path = clientnetbird.PathRelay
		}
	}
	return path
}
