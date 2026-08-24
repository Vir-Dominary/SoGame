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
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	daemonpb "sogame/internal/netbird/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeRPCClient struct {
	*fakeProfileClient
	statusResponse *daemonpb.StatusResponse
	loginRequest   *daemonpb.LoginRequest
	loginError     error
	upRequest      *daemonpb.UpRequest
	downCalls      int
	logoutRequest  *daemonpb.LogoutRequest
	stream         grpc.ServerStreamingClient[daemonpb.SystemEvent]
	streamError    error
}

func (f *fakeRPCClient) Login(_ context.Context, request *daemonpb.LoginRequest, _ ...grpc.CallOption) (*daemonpb.LoginResponse, error) {
	f.loginRequest = proto.Clone(request).(*daemonpb.LoginRequest)
	return &daemonpb.LoginResponse{}, f.loginError
}

func (f *fakeRPCClient) Up(_ context.Context, request *daemonpb.UpRequest, _ ...grpc.CallOption) (*daemonpb.UpResponse, error) {
	f.upRequest = request
	return &daemonpb.UpResponse{}, nil
}

func (f *fakeRPCClient) Status(context.Context, *daemonpb.StatusRequest, ...grpc.CallOption) (*daemonpb.StatusResponse, error) {
	return f.statusResponse, nil
}

func (f *fakeRPCClient) Down(context.Context, *daemonpb.DownRequest, ...grpc.CallOption) (*daemonpb.DownResponse, error) {
	f.downCalls++
	return &daemonpb.DownResponse{}, nil
}

func (f *fakeRPCClient) Logout(_ context.Context, request *daemonpb.LogoutRequest, _ ...grpc.CallOption) (*daemonpb.LogoutResponse, error) {
	f.logoutRequest = request
	return &daemonpb.LogoutResponse{}, nil
}

func (f *fakeRPCClient) SubscribeEvents(context.Context, *daemonpb.SubscribeRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.SystemEvent], error) {
	return f.stream, f.streamError
}

func newManagedRPCFake() *fakeRPCClient {
	return &fakeRPCClient{
		fakeProfileClient: &fakeProfileClient{
			profiles: []*daemonpb.Profile{{Id: "managed-id", Name: ManagedProfileName, IsActive: true}},
			activeID: "managed-id",
		},
		statusResponse: &daemonpb.StatusResponse{DaemonVersion: ExpectedVersion},
	}
}

func TestRPCAdapterEnrollUsesManagedProfileAndClearsSetupKey(t *testing.T) {
	fake := newManagedRPCFake()
	adapter := newRPCAdapter(fake, `DESKTOP\alice`, nil)
	key := NewSetupKey([]byte("secret-setup-key"))

	err := adapter.Enroll(context.Background(), EnrollmentRequest{
		ManagementURL: "https://legengen.top",
		ProfileID:     "managed-id",
		Hostname:      "gaming-pc",
		SetupKey:      key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.value != nil {
		t.Fatal("Setup Key was retained after enrollment")
	}
	if fake.loginRequest.GetSetupKey() != "secret-setup-key" || fake.loginRequest.GetProfileName() != "managed-id" {
		t.Fatalf("login request did not use the managed profile: %+v", fake.loginRequest)
	}
	if fake.loginRequest.GetManagementUrl() != "https://legengen.top" || fake.loginRequest.GetUsername() != `DESKTOP\alice` {
		t.Fatalf("login request=%+v", fake.loginRequest)
	}
}

func TestRPCAdapterEnrollmentErrorCannotExposeSetupKey(t *testing.T) {
	fake := newManagedRPCFake()
	fake.loginError = errors.New("daemon rejected secret-setup-key")
	key := NewSetupKey([]byte("secret-setup-key"))

	err := newRPCAdapter(fake, "alice", nil).Enroll(context.Background(), EnrollmentRequest{
		ManagementURL: "https://legengen.top",
		ProfileID:     "managed-id",
		SetupKey:      key,
	})
	if err == nil || strings.Contains(err.Error(), "secret-setup-key") {
		t.Fatalf("unsafe enrollment error=%v", err)
	}
	if key.value != nil {
		t.Fatal("Setup Key was retained after failed enrollment")
	}
}

func TestCreateProfileCleansOrphanManagedProfile(t *testing.T) {
	fake := newManagedRPCFake()
	fake.fakeProfileClient.profiles = []*daemonpb.Profile{{Id: "orphan-id", Name: ManagedProfileName, IsActive: true}}
	fake.fakeProfileClient.activeID = "orphan-id"
	fake.fakeProfileClient.addedID = "fresh-id"
	adapter := newRPCAdapter(fake, "alice", nil)
	profile, err := adapter.CreateProfile(context.Background(), ManagedProfileName)
	if err != nil {
		t.Fatalf("create after orphan cleanup failed: %v", err)
	}
	if profile.ID != "fresh-id" {
		t.Fatalf("profile=%+v", profile)
	}
	if len(fake.removed) != 1 || fake.removed[0] != "orphan-id" {
		t.Fatalf("orphan was not removed: %v", fake.removed)
	}
	if len(fake.selected) != 1 || fake.selected[0] != "default" {
		t.Fatalf("active managed profile was not switched to default first: %v", fake.selected)
	}
}

func TestCreateProfileFailsWhenOrphanCleanupFails(t *testing.T) {
	fake := newManagedRPCFake()
	fake.fakeProfileClient.profiles = []*daemonpb.Profile{{Id: "orphan-id", Name: ManagedProfileName}}
	fake.fakeProfileClient.activeID = "personal-id"
	fake.fakeProfileClient.removeErr = errors.New("daemon refused removal")
	adapter := newRPCAdapter(fake, "alice", nil)
	_, err := adapter.CreateProfile(context.Background(), ManagedProfileName)
	if !errors.Is(err, ErrManagedProfileConflict) {
		t.Fatalf("error=%v", err)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("orphan removal reported as attempted: %v", fake.removed)
	}
}

func TestRPCAdapterControlsOnlyValidatedManagedProfile(t *testing.T) {
	fake := newManagedRPCFake()
	adapter := newRPCAdapter(fake, "alice", nil)

	if err := adapter.Connect(context.Background(), "managed-id"); err != nil {
		t.Fatal(err)
	}
	if fake.upRequest.GetProfileName() != "managed-id" || fake.upRequest.GetUsername() != "alice" {
		t.Fatalf("up request=%+v", fake.upRequest)
	}
	if err := adapter.Disconnect(context.Background(), "managed-id"); err != nil {
		t.Fatal(err)
	}
	if fake.downCalls != 1 || len(fake.selected) != 1 || fake.selected[0] != "managed-id" {
		t.Fatalf("down calls=%d selected=%v", fake.downCalls, fake.selected)
	}
	if err := adapter.Deregister(context.Background(), "managed-id"); err != nil {
		t.Fatal(err)
	}
	if fake.logoutRequest.GetProfileName() != "managed-id" || fake.logoutRequest.GetUsername() != "alice" {
		t.Fatalf("logout request=%+v", fake.logoutRequest)
	}
}

func TestRPCAdapterRejectsUnmanagedProfileOperations(t *testing.T) {
	fake := newManagedRPCFake()
	adapter := newRPCAdapter(fake, "alice", nil)
	for _, operation := range []func() error{
		func() error { return adapter.Connect(context.Background(), "personal-id") },
		func() error { return adapter.Disconnect(context.Background(), "personal-id") },
		func() error { return adapter.Deregister(context.Background(), "personal-id") },
	} {
		if err := operation(); !errors.Is(err, ErrManagedProfileInconsistent) {
			t.Fatalf("error=%v", err)
		}
	}
	if fake.upRequest != nil || fake.downCalls != 0 || fake.logoutRequest != nil {
		t.Fatal("an unmanaged profile reached a daemon control operation")
	}
}

func TestRPCAdapterNormalizesStatusAndVersion(t *testing.T) {
	fake := newManagedRPCFake()
	fake.statusResponse = &daemonpb.StatusResponse{
		DaemonVersion: ExpectedVersion,
		Status:        "Connected",
		FullStatus: &daemonpb.FullStatus{
			LocalPeerState: &daemonpb.LocalPeerState{IP: "100.115.10.21"},
		},
	}
	adapter := newRPCAdapter(fake, "alice", nil)

	version, err := adapter.DaemonVersion(context.Background())
	if err != nil || version != ExpectedVersion {
		t.Fatalf("version=%q error=%v", version, err)
	}
	snapshot, err := adapter.Status(context.Background())
	if err != nil || snapshot.LocalNetBirdIP != "100.115.10.21" || snapshot.DaemonState != DaemonConnected {
		t.Fatalf("snapshot=%+v error=%v", snapshot, err)
	}
}

func TestRPCAdapterDeliversNormalizedEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := newManagedRPCFake()
	fake.stream = &fakeEventStream{ctx: ctx, events: []*daemonpb.SystemEvent{{
		Id:          "event-1",
		Severity:    daemonpb.SystemEvent_INFO,
		Category:    daemonpb.SystemEvent_SYSTEM,
		UserMessage: "Peer connected",
		Timestamp:   timestamppb.Now(),
	}}}

	events, failures := newRPCAdapter(fake, "alice", nil).Subscribe(ctx)
	event := <-events
	if event.ID != "event-1" || event.Severity != "info" || event.UserMessage != "Peer connected" {
		t.Fatalf("event=%+v", event)
	}
	if err := <-failures; err != nil {
		t.Fatalf("subscription error=%v", err)
	}
}

func TestRPCAdapterSanitizesSubscriptionErrors(t *testing.T) {
	fake := newManagedRPCFake()
	fake.streamError = status.Error(13, "secret daemon detail")
	events, failures := newRPCAdapter(fake, "alice", nil).Subscribe(context.Background())
	if _, ok := <-events; ok {
		t.Fatal("unexpected event")
	}
	err := <-failures
	if err == nil || strings.Contains(err.Error(), "secret daemon detail") {
		t.Fatalf("unsafe subscription error=%v", err)
	}
}

type fakeEventStream struct {
	ctx    context.Context
	events []*daemonpb.SystemEvent
	next   int
}

func (s *fakeEventStream) Recv() (*daemonpb.SystemEvent, error) {
	if s.next >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.next]
	s.next++
	return event, nil
}

func (s *fakeEventStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeEventStream) Trailer() metadata.MD         { return nil }
func (s *fakeEventStream) CloseSend() error             { return nil }
func (s *fakeEventStream) Context() context.Context     { return s.ctx }
func (s *fakeEventStream) SendMsg(any) error            { return nil }
func (s *fakeEventStream) RecvMsg(any) error            { return nil }
