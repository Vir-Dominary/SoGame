package session

import (
	"context"
	"errors"
	"testing"
)

func TestLeaveDeregistersRemovesManagedProfileAndClearsSavedRoom(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, metadata, codes := savedSessionService(t, adapter)

	snapshot, err := service.Leave(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateNoRoom || metadata.value != nil || codes.value != nil {
		t.Fatalf("snapshot=%+v metadata=%+v code=%q", snapshot, metadata.value, codes.value)
	}
	if len(adapter.calls) < 2 || adapter.calls[0] != "deregister" || adapter.calls[1] != "remove-profile" {
		t.Fatalf("leave calls=%v", adapter.calls)
	}
}

func TestLeavePreservesLocalRoomWhenRemoteDeregisterFails(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{"deregister": errors.New("remote unavailable")}}
	service, metadata, codes := savedSessionService(t, adapter)

	_, err := service.Leave(context.Background())
	if err == nil || metadata.value == nil || codes.value == nil || containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("error=%v metadata=%+v code=%q calls=%v", err, metadata.value, codes.value, adapter.calls)
	}
}

func TestLeavePreservesLocalRoomWhenRemoteProfileRemovalFails(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{"remove-profile": errors.New("profile removal failed")}}
	service, metadata, codes := savedSessionService(t, adapter)

	_, err := service.Leave(context.Background())
	if err == nil || metadata.value == nil || codes.value == nil {
		t.Fatalf("error=%v metadata=%+v code=%q", err, metadata.value, codes.value)
	}
}

func TestLeaveAttemptsBothLocalClearsAndReportsFailure(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, metadata, codes := savedSessionService(t, adapter)
	metadata.clearErr = errors.New("metadata locked")
	codes.clearErr = errors.New("code locked")

	_, err := service.Leave(context.Background())
	var transaction *TransactionError
	if !errors.As(err, &transaction) || transaction.CleanupFailures != 2 {
		t.Fatalf("error=%v", err)
	}
	if metadata.value == nil || codes.value == nil || !containsCall(adapter.calls, "deregister") || !containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("leave side effects metadata=%+v code=%q calls=%v", metadata.value, codes.value, adapter.calls)
	}
}

func TestLeaveDoesNotInvokeRoomAPIAdministrativeDisable(t *testing.T) {
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	service, _, _ := savedSessionService(t, adapter)
	if _, err := service.Leave(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range adapter.calls {
		if call == "disable-room" {
			t.Fatal("leave invoked an administrative room disable operation")
		}
	}
}
