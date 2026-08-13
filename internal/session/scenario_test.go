package session

import (
	"testing"

	clientnetbird "sogame/internal/netbird"
)

func TestSingleRoomStateScenarioIsDeterministic(t *testing.T) {
	p2p := clientnetbird.Peer{State: clientnetbird.PeerConnected, Path: clientnetbird.PathP2P}
	relay := clientnetbird.Peer{State: clientnetbird.PeerConnected, Path: clientnetbird.PathRelay}
	steps := []struct {
		name  string
		facts Facts
		state State
		path  clientnetbird.PathType
	}{
		{"initial", Facts{}, StateNoRoom, clientnetbird.PathNone},
		{"enrollment", Facts{EnrollmentInProgress: true}, StateEnrolling, clientnetbird.PathNone},
		{"control plane ready", Facts{RoomSaved: true, ControlPlaneReady: true}, StateControlPlaneConnected, clientnetbird.PathNone},
		{"empty room is healthy", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true}, StateWaitingForPeer, clientnetbird.PathNone},
		{"peer membership appears", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1}, StateConnectingPeer, clientnetbird.PathNone},
		{"direct tunnel", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1, DaemonPeers: []clientnetbird.Peer{p2p}}, StateConnectedP2P, clientnetbird.PathP2P},
		{"relay fallback", Facts{RoomSaved: true, ControlPlaneReady: true, MembershipKnown: true, OtherRoomPeerCount: 1, DaemonPeers: []clientnetbird.Peer{relay}}, StateConnectedRelay, clientnetbird.PathRelay},
		{"daemon or network outage", Facts{RoomSaved: true, ReconnectInProgress: true}, StateReconnecting, clientnetbird.PathNone},
		{"leave", Facts{}, StateNoRoom, clientnetbird.PathNone},
	}

	machine := NewMachine()
	for index, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			snapshot := machine.Apply(step.facts)
			if snapshot.State != step.state || snapshot.Path != step.path {
				t.Fatalf("step %d snapshot=%+v, want state=%s path=%s", index, snapshot, step.state, step.path)
			}
			if index > 0 && snapshot.Revision != uint64(index) {
				t.Fatalf("step %d revision=%d, want %d", index, snapshot.Revision, index)
			}
		})
	}
}
