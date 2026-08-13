package session

import (
	"context"
	"errors"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

type viewRoomAPI struct {
	peers roomapi.PeerList
	err   error
}

func (f *viewRoomAPI) Create(context.Context, *roomapi.CreateIntent) (roomapi.Enrollment, error) {
	return roomapi.Enrollment{}, errors.New("not used")
}

func (f *viewRoomAPI) Join(context.Context, string) (roomapi.Enrollment, error) {
	return roomapi.Enrollment{}, errors.New("not used")
}

func (f *viewRoomAPI) Peers(context.Context, string) (roomapi.PeerList, error) {
	return f.peers, f.err
}

func TestViewAggregatesDaemonAndRoomAPIWithoutPlaintextRoomCode(t *testing.T) {
	metadata := &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     time.Now().UTC().Add(-time.Hour),
	}}
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	adapter := &fakeSessionAdapter{status: clientnetbird.Snapshot{
		ManagementConnected: true,
		SignalConnected:     true,
		LocalNetBirdIP:      "100.115.10.21",
	}}
	rooms := &viewRoomAPI{peers: roomapi.PeerList{RoomID: "room-1", Peers: []roomapi.Peer{{
		ID: "peer-2", Name: "friend", NetBirdIP: "100.115.10.22", Connected: true,
	}}}}
	service := NewService(rooms, adapter, metadata, codes)

	view, err := service.View(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.RoomCodeMasked != "****-****-CCCC" || view.LocalNetBirdIP != "100.115.10.21" {
		t.Fatalf("view=%+v", view)
	}
	if len(view.Peers) != 1 || view.Peers[0].NetBirdIP != "100.115.10.22" || view.PeersStale {
		t.Fatalf("peers=%+v stale=%v", view.Peers, view.PeersStale)
	}
	if string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatal("view changed protected room code")
	}
}

func TestRevealRoomCodeIsExplicitAndDoesNotChangeSavedState(t *testing.T) {
	service, _, codes := savedSessionService(t, &fakeSessionAdapter{status: clientnetbird.Snapshot{}})

	roomCode, err := service.RevealRoomCode(context.Background())
	if err != nil || roomCode != "AAAA-BBBB-CCCC" {
		t.Fatalf("roomCode=%q error=%v", roomCode, err)
	}
	if string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatal("reveal changed protected room code")
	}
}

func TestViewKeepsCachedPeersStaleWhenRoomAPIFails(t *testing.T) {
	metadata := &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     time.Now().UTC().Add(-time.Hour),
	}}
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	rooms := &viewRoomAPI{peers: roomapi.PeerList{RoomID: "room-1", Peers: []roomapi.Peer{{ID: "peer-2"}}}}
	service := NewService(rooms, &fakeSessionAdapter{status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true}}, metadata, codes)
	if _, err := service.View(context.Background()); err != nil {
		t.Fatal(err)
	}
	rooms.err = errors.New("Room API unavailable")
	view, err := service.View(context.Background())
	if err != nil || !view.PeersStale || len(view.Peers) != 1 {
		t.Fatalf("view=%+v error=%v", view, err)
	}
}
