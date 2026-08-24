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

package netbird

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	daemonpb "sogame/internal/netbird/rpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNormalizeStatusSeparatesP2PRelayAndConnecting(t *testing.T) {
	handshake := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	response := &daemonpb.StatusResponse{
		Status:        "Connected",
		DaemonVersion: "0.74.7",
		FullStatus: &daemonpb.FullStatus{
			ManagementState: &daemonpb.ManagementState{URL: "https://legengen.top:443", Connected: true},
			SignalState:     &daemonpb.SignalState{URL: "https://legengen.top:443", Connected: true},
			LocalPeerState:  &daemonpb.LocalPeerState{IP: "100.115.10.21", Ipv6: "fd00::21"},
			Peers: []*daemonpb.PeerState{
				{IP: "100.115.10.22", ConnStatus: "Connected", Relayed: false, LastWireguardHandshake: timestamppb.New(handshake)},
				{IP: "100.115.10.23", ConnStatus: "Connected", Relayed: true, RelayAddress: "rels://relay.example"},
				{IP: "100.115.10.24", ConnStatus: "Connecting", Relayed: true},
			},
		},
	}

	got := NormalizeStatus(response)
	if got.DaemonVersion != "0.74.7" || !got.ManagementConnected || !got.SignalConnected {
		t.Fatalf("unexpected control-plane mapping: %+v", got)
	}
	if len(got.Peers) != 3 {
		t.Fatalf("peer count=%d", len(got.Peers))
	}
	if got.Peers[0].Path != PathP2P || !got.Peers[0].LastHandshake.Equal(handshake) {
		t.Fatalf("P2P peer=%+v", got.Peers[0])
	}
	if got.Peers[1].Path != PathRelay {
		t.Fatalf("Relay peer=%+v", got.Peers[1])
	}
	if got.Peers[2].Path != PathNone {
		t.Fatalf("connecting peer was incorrectly successful: %+v", got.Peers[2])
	}
}

func TestNormalizeProfilesUsesConcreteIDs(t *testing.T) {
	got := NormalizeProfiles(&daemonpb.ListProfilesResponse{Profiles: []*daemonpb.Profile{
		{Id: "aabbccdd", Name: "sogame-room", IsActive: true},
	}})
	if len(got) != 1 || got[0].ID != "aabbccdd" || got[0].Name != "sogame-room" || !got[0].IsActive {
		t.Fatalf("profiles=%+v", got)
	}
}

func TestSetupKeyStringIsAlwaysRedactedAndClearZerosMemory(t *testing.T) {
	source := []byte("2D989281-59FE-4762-874D-9E053D7E25C3")
	secret := NewSetupKey(source)
	if secret.String() != "[REDACTED]" {
		t.Fatalf("secret string=%q", secret.String())
	}
	secret.Clear()
	if secret.value != nil {
		t.Fatal("secret retained bytes after Clear")
	}
}

func TestSetupKeyCannotBeFormattedLoggedOrSerialized(t *testing.T) {
	secretValue := "secret-setup-key"
	secret := NewSetupKey([]byte(secretValue))
	defer secret.Clear()
	for _, formatted := range []string{
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%+v", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprintf("%q", secret),
	} {
		if formatted != "[REDACTED]" || bytes.Contains([]byte(formatted), []byte(secretValue)) {
			t.Fatalf("unsafe formatted Setup Key=%q", formatted)
		}
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	logger.Info("enrollment", "credential", secret)
	if bytes.Contains(logOutput.Bytes(), []byte(secretValue)) || !bytes.Contains(logOutput.Bytes(), []byte("[REDACTED]")) {
		t.Fatalf("unsafe log output=%q", logOutput.String())
	}
	if payload, err := json.Marshal(secret); err == nil {
		t.Fatalf("Setup Key serialized: %s", payload)
	}
}
