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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
	"sogame/internal/roomapi"
	"sogame/internal/securestore"
)

type fakeSessionAdapter struct {
	mu                   sync.Mutex
	calls                []string
	fail                 map[string]error
	status               clientnetbird.Snapshot
	enrollHook           func()
	cleanupContextActive bool
}

func (f *fakeSessionAdapter) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.fail[call]
}

func (f *fakeSessionAdapter) DaemonVersion(context.Context) (string, error) {
	return clientnetbird.ExpectedVersion, nil
}
func (f *fakeSessionAdapter) Status(context.Context) (clientnetbird.Snapshot, error) {
	return f.status, f.record("status")
}
func (f *fakeSessionAdapter) ListProfiles(context.Context) ([]clientnetbird.Profile, error) {
	return nil, nil
}
func (f *fakeSessionAdapter) ActiveProfile(context.Context) (clientnetbird.Profile, error) {
	return clientnetbird.Profile{}, nil
}
func (f *fakeSessionAdapter) CreateProfile(context.Context, string) (clientnetbird.Profile, error) {
	if err := f.record("create-profile"); err != nil {
		return clientnetbird.Profile{}, err
	}
	return clientnetbird.Profile{ID: "profile-1", Name: clientnetbird.ManagedProfileName}, nil
}
func (f *fakeSessionAdapter) SelectProfile(context.Context, string) error { return nil }
func (f *fakeSessionAdapter) RemoveProfile(context.Context, string) error {
	return f.record("remove-profile")
}
func (f *fakeSessionAdapter) Enroll(_ context.Context, request clientnetbird.EnrollmentRequest) error {
	if request.SetupKey == nil || request.SetupKey.String() != "[REDACTED]" || request.ProfileID != "profile-1" {
		return errors.New("invalid enrollment request")
	}
	if f.enrollHook != nil {
		f.enrollHook()
	}
	return f.record("enroll")
}
func (f *fakeSessionAdapter) Connect(context.Context, string) error {
	return f.record("connect")
}
func (f *fakeSessionAdapter) Disconnect(context.Context, string) error {
	return f.record("disconnect")
}
func (f *fakeSessionAdapter) Deregister(ctx context.Context, _ string) error {
	if ctx.Err() == nil {
		f.mu.Lock()
		f.cleanupContextActive = true
		f.mu.Unlock()
	}
	return f.record("deregister")
}
func (f *fakeSessionAdapter) Subscribe(context.Context) (<-chan clientnetbird.Event, <-chan error) {
	return make(chan clientnetbird.Event), make(chan error)
}

type memoryMetadata struct {
	value    *securestore.RoomMetadata
	saveErr  error
	clearErr error
}

func (m *memoryMetadata) Load() (securestore.RoomMetadata, error) {
	if m.value == nil {
		return securestore.RoomMetadata{}, securestore.ErrNoRoomMetadata
	}
	return *m.value, nil
}
func (m *memoryMetadata) Save(value securestore.RoomMetadata) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.value = &value
	return nil
}
func (m *memoryMetadata) Clear() error {
	if m.clearErr != nil {
		return m.clearErr
	}
	m.value = nil
	return nil
}

type memoryRoomCode struct {
	value    []byte
	loadErr  error
	saveErr  error
	clearErr error
}

func (m *memoryRoomCode) Load() ([]byte, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.value == nil {
		return nil, securestore.ErrNoProtectedRoomCode
	}
	return append([]byte(nil), m.value...), nil
}
func (m *memoryRoomCode) Save(value []byte) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.value = append([]byte(nil), value...)
	return nil
}
func (m *memoryRoomCode) Clear() error {
	if m.clearErr != nil {
		return m.clearErr
	}
	clearBytes(m.value)
	m.value = nil
	return nil
}

func newSessionRoomAPI(t *testing.T, handler http.HandlerFunc) (*roomapi.Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := roomapi.NewClient(server.URL, server.Client())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, server
}

func successfulRoomHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/rooms":
			if request.Header.Get("Idempotency-Key") == "" {
				t.Error("create request has no idempotency key")
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"room_id":"room-1","room_code":"AAAA-BBBB-CCCC","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/join":
			_, _ = response.Write([]byte(`{"room_id":"room-1","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		default:
			http.NotFound(response, request)
		}
	}
}

func TestCreateAndJoinCommitSingleRoomTransaction(t *testing.T) {
	for _, mode := range []string{"create", "join"} {
		t.Run(mode, func(t *testing.T) {
			rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
			defer server.Close()
			adapter := &fakeSessionAdapter{
				fail:   map[string]error{},
				status: clientnetbird.Snapshot{ManagementConnected: true, SignalConnected: true},
			}
			metadata := &memoryMetadata{}
			codes := &memoryRoomCode{}
			service := NewService(rooms, adapter, metadata, codes)
			service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

			var snapshot Snapshot
			var err error
			if mode == "create" {
				snapshot, err = service.Create(context.Background(), "gaming-pc")
			} else {
				snapshot, err = service.Join(context.Background(), "AAAA-BBBB-CCCC", "gaming-pc")
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.State != StateControlPlaneConnected || metadata.value == nil || metadata.value.ProfileID != "profile-1" {
				t.Fatalf("snapshot=%+v metadata=%+v", snapshot, metadata.value)
			}
			if string(codes.value) != "AAAA-BBBB-CCCC" {
				t.Fatalf("room code=%q", codes.value)
			}
			if containsCall(adapter.calls, "deregister") || containsCall(adapter.calls, "remove-profile") {
				t.Fatalf("successful transaction was compensated: %v", adapter.calls)
			}
		})
	}
}

func TestRoomAPIRejectionCreatesNoLocalState(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	metadata := &memoryMetadata{}
	codes := &memoryRoomCode{}
	service := NewService(rooms, adapter, metadata, codes)

	snapshot, err := service.Join(context.Background(), "AAAA-BBBB-CCCC", "gaming-pc")
	var httpError *roomapi.HTTPError
	if !errors.As(err, &httpError) || snapshot.State != StateRecoverableError {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
	if len(adapter.calls) != 0 || metadata.value != nil || codes.value != nil {
		t.Fatalf("partial local state: calls=%v metadata=%v code=%q", adapter.calls, metadata.value, codes.value)
	}
}

func TestEnrollmentAndConnectFailuresAreCompensated(t *testing.T) {
	for _, operation := range []string{"enroll", "connect"} {
		t.Run(operation, func(t *testing.T) {
			rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
			defer server.Close()
			adapter := &fakeSessionAdapter{fail: map[string]error{operation: errors.New("operation failed")}}
			metadata := &memoryMetadata{}
			codes := &memoryRoomCode{}
			service := NewService(rooms, adapter, metadata, codes)

			_, err := service.Create(context.Background(), "gaming-pc")
			var transaction *TransactionError
			if !errors.As(err, &transaction) {
				t.Fatalf("error=%v", err)
			}
			if !containsCall(adapter.calls, "deregister") || !containsCall(adapter.calls, "remove-profile") {
				t.Fatalf("cleanup calls=%v", adapter.calls)
			}
			if metadata.value != nil || codes.value != nil {
				t.Fatal("failed transaction persisted local room state")
			}
		})
	}
}

func TestMetadataFailureClearsProtectedCodeAndDaemonIdentity(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	adapter := &fakeSessionAdapter{fail: map[string]error{}}
	metadata := &memoryMetadata{saveErr: errors.New("disk full")}
	codes := &memoryRoomCode{}
	service := NewService(rooms, adapter, metadata, codes)

	_, err := service.Create(context.Background(), "gaming-pc")
	if err == nil || codes.value != nil || metadata.value != nil {
		t.Fatalf("error=%v code=%q metadata=%v", err, codes.value, metadata.value)
	}
	if !containsCall(adapter.calls, "deregister") || !containsCall(adapter.calls, "remove-profile") {
		t.Fatalf("cleanup calls=%v", adapter.calls)
	}
}

func TestCompensationUsesCleanupContextAfterCommandCancellation(t *testing.T) {
	rooms, server := newSessionRoomAPI(t, successfulRoomHandler(t))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &fakeSessionAdapter{fail: map[string]error{"enroll": context.Canceled}, enrollHook: cancel}
	service := NewService(rooms, adapter, &memoryMetadata{}, &memoryRoomCode{})

	_, err := service.Create(ctx, "gaming-pc")
	if err == nil || !adapter.cleanupContextActive {
		t.Fatalf("error=%v cleanup context active=%v calls=%v", err, adapter.cleanupContextActive, adapter.calls)
	}
}

func TestSavedOrConflictingStorageBlocksRoomAPI(t *testing.T) {
	apiCalls := 0
	rooms, server := newSessionRoomAPI(t, func(response http.ResponseWriter, request *http.Request) {
		apiCalls++
		successfulRoomHandler(t)(response, request)
	})
	defer server.Close()
	for _, test := range []struct {
		name     string
		metadata *memoryMetadata
		codes    *memoryRoomCode
		want     error
	}{
		{
			name:     "saved",
			metadata: &memoryMetadata{value: &securestore.RoomMetadata{RoomID: "room-1", Version: securestore.CurrentMetadataVersion, ManagementURL: "https://legengen.top", ProfileID: "profile-1", CreatedAt: time.Now().UTC().Add(-time.Hour)}},
			codes:    &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")},
			want:     ErrRoomAlreadySaved,
		},
		{
			name:     "orphan code",
			metadata: &memoryMetadata{},
			codes:    &memoryRoomCode{value: []byte("AAAA-BBBB-CCCC")},
			want:     ErrStoredStateConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(rooms, &fakeSessionAdapter{fail: map[string]error{}}, test.metadata, test.codes)
			_, err := service.Create(context.Background(), "gaming-pc")
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if apiCalls != 0 {
		t.Fatalf("Room API called %d times for blocked transactions", apiCalls)
	}
}

func containsCall(calls []string, wanted string) bool {
	for _, call := range calls {
		if call == wanted {
			return true
		}
	}
	return false
}
