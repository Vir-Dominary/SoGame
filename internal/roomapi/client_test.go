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

package roomapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clientnetbird "sogame/internal/netbird"
)

func TestClientCreateJoinAndListPeers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/rooms":
			if request.Method != http.MethodPost || !strings.HasPrefix(request.Header.Get("Idempotency-Key"), "sogame-room-") {
				t.Errorf("create request=%s headers=%v", request.Method, request.Header)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"room_id":"room-1","room_code":"7X4K-329B-YY95","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/join":
			if request.Method != http.MethodPost {
				t.Errorf("join method=%s", request.Method)
			}
			_, _ = response.Write([]byte(`{"room_id":"room-1","management_url":"https://legengen.top","setup_key":"secret-key"}`))
		case "/rooms/7X4K-329B-YY95/peers":
			_, _ = response.Write([]byte(`{"room_id":"room-1","peers":[{"id":"peer-1","name":"pc","netbird_ip":"100.115.10.21","connected":true,"hostname":"gaming-pc"}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	intent, err := NewCreateIntent()
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if created.RoomID != "room-1" || created.RoomCode != "7X4K-329B-YY95" {
		t.Fatalf("created=%+v", created)
	}
	if err := created.ConsumeSetupKey(func(key *clientnetbird.SetupKey) error {
		if key.String() != "[REDACTED]" {
			t.Fatalf("Setup Key string=%q", key.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	joined, err := client.Join(context.Background(), " 7X4K-329B-YY95 ")
	if err != nil {
		t.Fatal(err)
	}
	if joined.RoomID != "room-1" || joined.RoomCode != "7X4K-329B-YY95" {
		t.Fatalf("joined=%+v", joined)
	}
	if err := joined.ConsumeSetupKey(func(*clientnetbird.SetupKey) error { return nil }); err != nil {
		t.Fatal(err)
	}
	peers, err := client.Peers(context.Background(), joined.RoomCode)
	if err != nil {
		t.Fatal(err)
	}
	if peers.RoomID != "room-1" || len(peers.Peers) != 1 || peers.Peers[0].NetBirdIP != "100.115.10.21" {
		t.Fatalf("peers=%+v", peers)
	}
}

func TestClientReturnsTypedSafeHTTPErrors(t *testing.T) {
	for _, test := range []struct {
		status int
		code   ErrorCode
	}{
		{http.StatusBadRequest, ErrorInvalidRequest},
		{http.StatusNotFound, ErrorRoomUnavailable},
		{http.StatusConflict, ErrorOperationConflict},
		{http.StatusTooManyRequests, ErrorRateLimited},
		{http.StatusBadGateway, ErrorServiceUnavailable},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Retry-After", "3")
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(`{"error":"rejected secret-setup-key"}`))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			client.retryDelays = nil
			_, err = client.Join(context.Background(), "7X4K-329B-YY95")
			var httpError *HTTPError
			if !errors.As(err, &httpError) || httpError.Code != test.code {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(err.Error(), "secret-setup-key") {
				t.Fatalf("unsafe error=%v", err)
			}
			if test.status == http.StatusTooManyRequests && (!httpError.Transient() || httpError.RetryAfter != 3*time.Second) {
				t.Fatalf("rate limit error=%+v", httpError)
			}
		})
	}
}

func TestClientRejectsOversizedAndMalformedResponses(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("x", 65),
		`{"room_id":"room-1"}`,
		`not-json`,
	} {
		t.Run(body[:min(len(body), 12)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(body))
			}))
			defer server.Close()
			client, err := newClient(server.URL, server.Client(), time.Second, 64)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Join(context.Background(), "AAAA-BBBB-CCCC")
			var protocol *ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestClientAppliesRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := newClient(server.URL, server.Client(), 10*time.Millisecond, defaultResponseLimit)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Peers(context.Background(), "AAAA-BBBB-CCCC")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestClientRejectsUnsupportedSchemesAndRedirects(t *testing.T) {
	if _, err := NewClient("ftp://example.com", nil); err == nil {
		t.Fatal("unsupported Room API URL scheme was accepted")
	}
	redirectTargetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled = true
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Join(context.Background(), "AAAA-BBBB-CCCC")
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("error=%v", err)
	}
	if redirectTargetCalled {
		t.Fatal("Room API client followed a redirect containing a room code")
	}
}

func TestClientNormalizesAndValidatesRoomCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rooms/7X4K-329B-YY95/peers" {
			t.Errorf("path=%q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"room_id":"room-1","peers":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Peers(context.Background(), " 7x4k-329b-yy95 "); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "AAAA-BBBB", "AAAA/BBBB/CCCC", "AAAA-BBBB-CCC_"} {
		if _, err := client.Peers(context.Background(), invalid); err == nil {
			t.Fatalf("invalid room code %q was accepted", invalid)
		}
	}
}
