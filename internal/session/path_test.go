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
