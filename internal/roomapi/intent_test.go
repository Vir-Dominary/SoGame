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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	clientnetbird "sogame/internal/netbird"
)

func TestCreateIntentKeysAreUniqueAndBackendOwned(t *testing.T) {
	seen := make(map[string]struct{})
	for range 100 {
		intent, err := NewCreateIntent()
		if err != nil {
			t.Fatal(err)
		}
		if intent.key == "" {
			t.Fatal("empty intent key")
		}
		if _, duplicate := seen[intent.key]; duplicate {
			t.Fatalf("duplicate intent key %q", intent.key)
		}
		seen[intent.key] = struct{}{}
	}
}

func TestCreateIntentReusesKeyAfterFailureAndSealsAfterSuccess(t *testing.T) {
	var (
		mu       sync.Mutex
		keys     []string
		requests int
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		requests++
		current := requests
		mu.Unlock()
		if current == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"room_id":"room-1","room_code":"AAAA-BBBB-CCCC","management_url":"https://legengen.top","setup_key":"secret-key"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryDelays = nil
	intent, err := NewCreateIntent()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Create(context.Background(), intent); err == nil {
		t.Fatal("first create unexpectedly succeeded")
	}
	enrollment, err := client.Create(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := enrollment.ConsumeSetupKey(func(*clientnetbird.SetupKey) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("idempotency keys=%v", keys)
	}
	if _, err := client.Create(context.Background(), intent); !errors.Is(err, ErrCreateIntentComplete) {
		t.Fatalf("completed intent error=%v", err)
	}
}

func TestCreateIntentRejectsConcurrentUse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryDelays = nil
	intent, err := NewCreateIntent()
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = client.Create(context.Background(), intent)
	}()
	<-started
	if _, err := client.Create(context.Background(), intent); !errors.Is(err, ErrCreateIntentInFlight) {
		t.Fatalf("concurrent intent error=%v", err)
	}
	close(release)
	<-firstDone
}

func TestCreateIntentReportsEntropyFailure(t *testing.T) {
	if _, err := newCreateIntent(io.LimitReader(&zeroReader{}, 0)); err == nil {
		t.Fatal("entropy failure was ignored")
	}
}

type zeroReader struct{}

func (*zeroReader) Read([]byte) (int, error) { return 0, io.EOF }
