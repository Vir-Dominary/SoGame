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
	"strings"

	daemonpb "sogame/internal/netbird/rpc"
)

func NormalizeStatus(response *daemonpb.StatusResponse) Snapshot {
	result := Snapshot{Peers: []Peer{}}
	if response == nil {
		return result
	}
	result.DaemonVersion = response.GetDaemonVersion()
	result.DaemonState = normalizeDaemonState(response.GetStatus())
	full := response.GetFullStatus()
	if full == nil {
		return result
	}
	if management := full.GetManagementState(); management != nil {
		result.ManagementURL = management.GetURL()
		result.ManagementConnected = management.GetConnected()
	}
	if signal := full.GetSignalState(); signal != nil {
		result.SignalURL = signal.GetURL()
		result.SignalConnected = signal.GetConnected()
	}
	if local := full.GetLocalPeerState(); local != nil {
		result.LocalNetBirdIP = local.GetIP()
		result.LocalNetBirdIPv6 = local.GetIpv6()
	}
	result.Peers = make([]Peer, 0, len(full.GetPeers()))
	for _, source := range full.GetPeers() {
		if source == nil {
			continue
		}
		state := normalizePeerState(source.GetConnStatus())
		path := PathNone
		if state == PeerConnected {
			path = PathP2P
			if source.GetRelayed() {
				path = PathRelay
			}
		}
		peer := Peer{
			PublicKey:     source.GetPubKey(),
			FQDN:          source.GetFqdn(),
			NetBirdIP:     source.GetIP(),
			NetBirdIPv6:   source.GetIpv6(),
			State:         state,
			Path:          path,
			RelayAddress:  source.GetRelayAddress(),
			BytesReceived: source.GetBytesRx(),
			BytesSent:     source.GetBytesTx(),
		}
		if handshake := source.GetLastWireguardHandshake(); handshake != nil && handshake.IsValid() {
			peer.LastHandshake = handshake.AsTime()
		}
		result.Peers = append(result.Peers, peer)
	}
	return result
}

func NormalizeProfiles(response *daemonpb.ListProfilesResponse) []Profile {
	if response == nil {
		return []Profile{}
	}
	profiles := make([]Profile, 0, len(response.GetProfiles()))
	for _, source := range response.GetProfiles() {
		if source == nil {
			continue
		}
		profiles = append(profiles, Profile{ID: source.GetId(), Name: source.GetName(), IsActive: source.GetIsActive()})
	}
	return profiles
}

func NormalizeEvent(source *daemonpb.SystemEvent) Event {
	if source == nil {
		return Event{}
	}
	event := Event{
		ID:          source.GetId(),
		Severity:    strings.ToLower(source.GetSeverity().String()),
		Category:    strings.ToLower(source.GetCategory().String()),
		UserMessage: source.GetUserMessage(),
	}
	if timestamp := source.GetTimestamp(); timestamp != nil && timestamp.IsValid() {
		event.OccurredAt = timestamp.AsTime()
	}
	return event
}

func normalizeDaemonState(value string) DaemonState {
	switch strings.ToLower(value) {
	case "idle":
		return DaemonIdle
	case "connecting":
		return DaemonConnecting
	case "connected":
		return DaemonConnected
	case "needslogin":
		return DaemonNeedsLogin
	case "loginfailed":
		return DaemonLoginFailed
	case "sessionexpired":
		return DaemonSessionExpired
	default:
		return DaemonUnknown
	}
}

func normalizePeerState(value string) PeerState {
	switch strings.ToLower(value) {
	case "idle":
		return PeerIdle
	case "connecting":
		return PeerConnecting
	case "connected":
		return PeerConnected
	default:
		return PeerUnknown
	}
}
