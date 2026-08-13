package session

import (
	"testing"

	clientnetbird "sogame/internal/netbird"
)

func TestDeriveAllNormalizedStates(t *testing.T) {
	p2pPeer := clientnetbird.Peer{State: clientnetbird.PeerConnected, Path: clientnetbird.PathP2P}
	relayPeer := clientnetbird.Peer{State: clientnetbird.PeerConnected, Path: clientnetbird.PathRelay}
	for _, test := range []struct {
		name  string
		facts Facts
		state State
		path  clientnetbird.PathType
	}{
		{"no room", Facts{}, StateNoRoom, clientnetbird.PathNone},
		{"enrolling", Facts{EnrollmentInProgress: true}, StateEnrolling, clientnetbird.PathNone},
		{"control plane", Facts{RoomSaved: true, ControlPlaneReady: true}, StateControlPlaneConnected, clientnetbird.PathNone},
		{"waiting", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true}, StateWaitingForPeer, clientnetbird.PathNone},
		{"connecting peer", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1}, StateConnectingPeer, clientnetbird.PathNone},
		{"P2P", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1, DaemonPeers: []clientnetbird.Peer{p2pPeer}}, StateConnectedP2P, clientnetbird.PathP2P},
		{"Relay", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1, DaemonPeers: []clientnetbird.Peer{relayPeer}}, StateConnectedRelay, clientnetbird.PathRelay},
		{"reconnecting command", Facts{RoomSaved: true, ReconnectInProgress: true}, StateReconnecting, clientnetbird.PathNone},
		{"control plane outage", Facts{RoomSaved: true}, StateReconnecting, clientnetbird.PathNone},
		{"recoverable error", Facts{RecoverableError: true}, StateRecoverableError, clientnetbird.PathNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, path := Derive(test.facts)
			if state != test.state || path != test.path {
				t.Fatalf("state=%s path=%s, want state=%s path=%s", state, path, test.state, test.path)
			}
		})
	}
}

func TestDerivePrefersP2PButAcceptsRelay(t *testing.T) {
	facts := Facts{
		RoomSaved:          true,
		ControlPlaneReady:  true,
		MembershipKnown:    true,
		OtherRoomPeerCount: 2,
		DaemonPeers: []clientnetbird.Peer{
			{State: clientnetbird.PeerConnected, Path: clientnetbird.PathRelay},
			{State: clientnetbird.PeerConnected, Path: clientnetbird.PathP2P},
		},
	}
	state, path := Derive(facts)
	if state != StateConnectedP2P || path != clientnetbird.PathP2P {
		t.Fatalf("state=%s path=%s", state, path)
	}
}

func TestDeriveNeverTreatsControlPlaneOrConnectingPeerAsTunnelSuccess(t *testing.T) {
	facts := Facts{
		RoomSaved:          true,
		ControlPlaneReady:  true,
		MembershipKnown:    true,
		OtherRoomPeerCount: 1,
		DaemonPeers: []clientnetbird.Peer{{
			State: clientnetbird.PeerConnecting,
			Path:  clientnetbird.PathRelay,
		}},
	}
	state, path := Derive(facts)
	if state != StateConnectingPeer || path != clientnetbird.PathNone {
		t.Fatalf("state=%s path=%s", state, path)
	}
}

func TestMachineRevisionChangesOnlyWhenPresentationChanges(t *testing.T) {
	machine := NewMachine()
	initial := machine.Snapshot()
	if initial.Revision != 0 || initial.State != StateNoRoom {
		t.Fatalf("initial=%+v", initial)
	}
	unchanged := machine.Apply(Facts{})
	if unchanged.Revision != 0 {
		t.Fatalf("unchanged revision=%d", unchanged.Revision)
	}
	waiting := machine.Apply(Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true})
	if waiting.Revision != 1 || waiting.State != StateWaitingForPeer {
		t.Fatalf("waiting=%+v", waiting)
	}
	stillWaiting := machine.Apply(Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: -1})
	if stillWaiting.Revision != waiting.Revision {
		t.Fatalf("equivalent presentation changed revision: %+v", stillWaiting)
	}
}

func TestStatePrecedenceKeepsErrorsAndCommandsDeterministic(t *testing.T) {
	all := Facts{
		RoomSaved:            true,
		EnrollmentInProgress: true,
		ReconnectInProgress:  true,
		RecoverableError:     true,
		ControlPlaneReady:    true,
		MembershipKnown:      true,
		OtherRoomPeerCount:   1,
		DaemonPeers:          []clientnetbird.Peer{{State: clientnetbird.PeerConnected, Path: clientnetbird.PathP2P}},
	}
	if state, _ := Derive(all); state != StateRecoverableError {
		t.Fatalf("state=%s", state)
	}
	all.RecoverableError = false
	if state, _ := Derive(all); state != StateEnrolling {
		t.Fatalf("state=%s", state)
	}
	all.EnrollmentInProgress = false
	if state, _ := Derive(all); state != StateReconnecting {
		t.Fatalf("state=%s", state)
	}
}
