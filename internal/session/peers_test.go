package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

type fakePeerAPI struct {
	mu       sync.Mutex
	calls    []string
	response roomapi.PeerList
	err      error
}

func (f *fakePeerAPI) Peers(_ context.Context, roomCode string) (roomapi.PeerList, error) {
	f.mu.Lock()
	f.calls = append(f.calls, roomCode)
	f.mu.Unlock()
	return f.response, f.err
}

func TestPeerRefresherKeepsPreviousPeersAndMarksStaleOnFailure(t *testing.T) {
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	api := &fakePeerAPI{response: roomapi.PeerList{RoomID: "room-1", Peers: []roomapi.Peer{{ID: "peer-1", NetBirdIP: "100.115.10.21"}}}}
	refresher := NewPeerRefresher(api, codes)
	refresher.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	snapshot, err := refresher.Refresh(context.Background())
	if err != nil || snapshot.Stale || len(snapshot.Peers) != 1 || snapshot.LastRefreshAt.IsZero() {
		t.Fatalf("first snapshot=%+v error=%v", snapshot, err)
	}
	api.err = errors.New("peer refresh unavailable")
	failed, err := refresher.Refresh(context.Background())
	if err == nil || !failed.Stale || len(failed.Peers) != 1 || failed.Peers[0].ID != "peer-1" {
		t.Fatalf("failed snapshot=%+v error=%v", failed, err)
	}
	if len(api.calls) != 2 || api.calls[0] != "AAAA-BBBB-CCCC" || api.calls[1] != "AAAA-BBBB-CCCC" {
		t.Fatalf("room code calls=%v", api.calls)
	}
	if codes.value == nil || string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatal("refresher altered protected store value")
	}
}

func TestPeerRefresherMarksMissingRoomStaleWithoutCallingAPI(t *testing.T) {
	codes := &memoryRoomCode{}
	api := &fakePeerAPI{}
	refresher := NewPeerRefresher(api, codes)

	snapshot, err := refresher.Refresh(context.Background())
	if !errors.Is(err, securestore.ErrNoProtectedRoomCode) || !snapshot.Stale || len(api.calls) != 0 {
		t.Fatalf("snapshot=%+v error=%v calls=%v", snapshot, err, api.calls)
	}
}

func TestPeerRefresherWatchUsesForegroundInterval(t *testing.T) {
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	api := &fakePeerAPI{response: roomapi.PeerList{RoomID: "room-1", Peers: []roomapi.Peer{}}}
	refresher := NewPeerRefresher(api, codes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := refresher.Watch(ctx, PeerRefreshForeground)
	first := <-updates
	if first.Stale {
		t.Fatalf("initial snapshot=%+v", first)
	}
	select {
	case <-updates:
		t.Fatal("foreground watcher refreshed sooner than its configured interval")
	case <-time.After(20 * time.Millisecond):
	}
	if got := peerRefreshInterval(PeerRefreshForeground); got != ForegroundPeerRefreshInterval {
		t.Fatalf("foreground interval=%s", got)
	}
}

func TestPeerRefresherUsesThirtySecondTrayInterval(t *testing.T) {
	if got := peerRefreshInterval(PeerRefreshTray); got != 30*time.Second {
		t.Fatalf("tray interval=%s", got)
	}
}
