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
	"testing"

	clientnetbird "sogame/internal/netbird"
)

func TestFactsFromDaemonUsesOnlyOfficialReportedPath(t *testing.T) {
	for _, test := range []struct {
		name  string
		path  clientnetbird.PathType
		state State
	}{
		{name: "P2P", path: clientnetbird.PathP2P, state: StateConnectedP2P},
		{name: "Relay", path: clientnetbird.PathRelay, state: StateConnectedRelay},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := FactsFromDaemon(true, true, 1, clientnetbird.Snapshot{
				ManagementConnected: true,
				SignalConnected:     true,
				Peers: []clientnetbird.Peer{{
					State: clientnetbird.PeerConnected,
					Path:  test.path,
				}},
			})
			state, path := Derive(facts)
			if state != test.state || path != test.path {
				t.Fatalf("state=%s path=%s", state, path)
			}
		})
	}
}

func TestFactsFromDaemonDoesNotTreatControlPlaneAsTunnel(t *testing.T) {
	facts := FactsFromDaemon(true, true, 1, clientnetbird.Snapshot{
		ManagementConnected: true,
		SignalConnected:     true,
		Peers:               []clientnetbird.Peer{{State: clientnetbird.PeerConnecting, Path: clientnetbird.PathRelay}},
	})
	state, path := Derive(facts)
	if state != StateConnectingPeer || path != clientnetbird.PathNone {
		t.Fatalf("state=%s path=%s", state, path)
	}
}
