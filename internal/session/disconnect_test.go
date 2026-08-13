package session

import (
	"context"
	"errors"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/securestore"
)

func savedSessionService(t *testing.T, adapter *fakeSessionAdapter) (*Service, *memoryMetadata, *memoryRoomCode) {
	t.Helper()
	metadata := &memoryMetadata{value: &securestore.RoomMetadata{
		Version:       securestore.CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     "profile-1",
		CreatedAt:     testCreatedAt,
	}}
	codes := &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")}
	return NewService(nil, adapter, metadata, codes), metadata, codes
}

var testCreatedAt = time.Now().UTC().Add(-time.Hour)

func TestDisconnectPreservesSavedRoomIdentity(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}, status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true}}
	service, metadata, codes := savedSessionService(t, adapter)

	snapshot, err := service.Disconnect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateControlPlaneConnected || !containsCall(adapter.calls, "disconnect") {
		t.Fatalf("snapshot=%+v calls=%v", snapshot, adapter.calls)
	}
	if metadata.value == nil || string(codes.value) != "AAAA-BBBB-CCCC" {
		t.Fatal("disconnect removed saved room identity")
	}
}

func TestReconnectUsesExistingProfileWithoutRoomAPIOrEnrollment(t *testing.T) {
	adapter := &fakeSessionAdapter{
		fail:   map[string]error{},
		status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true},
	}
	service, _, _ := savedSessionService(t, adapter)

	snapshot, err := service.Reconnect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateControlPlaneConnected {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if containsCall(adapter.calls, "enroll") || containsCall(adapter.calls, "create-profile") || containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("reconnect changed identity: %v", adapter.calls)
	}
	if !containsCall(adapter.calls, "connect") || !containsCall(adapter.calls, "status") {
		t.Fatalf("reconnect calls=%v", adapter.calls)
	}
}

func TestReconnectRequiresConsistentSavedRoom(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, metadata, codes := savedSessionService(t, adapter)
	codes.value = nil

	_, err := service.Reconnect(context.Background())
	if !errors.Is(err, ErrStoredStateConflict) || metadata.value == nil || len(adapter.calls) != 0 {
		t.Fatalf("error=%v metadata=%+v calls=%v", err, metadata.value, adapter.calls)
	}
}
