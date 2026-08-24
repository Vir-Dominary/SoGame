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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestClientJoinCarriesUniqueIdempotencyKeys(t *testing.T) {
	var (
		mu   sync.Mutex
		keys []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		mu.Unlock()
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"room_id":"room-1","management_url":"https://legengen.top","setup_key":"secret-key"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	first, err := client.Join(context.Background(), "AAAA-BBBB-CCCC")
	if err != nil {
		t.Fatal(err)
	}
	first.DiscardSetupKey()
	second, err := client.Join(context.Background(), "AAAA-BBBB-CCCC")
	if err != nil {
		t.Fatal(err)
	}
	second.DiscardSetupKey()

	if len(keys) != 2 || keys[0] == "" || keys[1] == "" || keys[0] == keys[1] {
		t.Fatalf("idempotency keys=%v", keys)
	}
	for _, key := range keys {
		if len(key) < len("sogame-join-")+16 {
			t.Fatalf("idempotency key too short: %q", key)
		}
	}
}

func TestClientPerAttemptTimeoutAbortsSlowServer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		time.Sleep(6 * time.Second)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	// 尝试级超时(defaultPerAttemptTimeout)必须早于整体预算生效,
	// 不能重试,直接把错误上抛给调用方。
	client.wait = func(context.Context, time.Duration) error {
		t.Fatal("slow server triggered retry wait")
		return nil
	}

	start := time.Now()
	_, err = client.Join(context.Background(), "AAAA-BBBB-CCCC")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if elapsed < defaultPerAttemptTimeout || elapsed >= defaultPerAttemptTimeout+2*time.Second {
		t.Fatalf("elapsed=%s, want ~%s", elapsed, defaultPerAttemptTimeout)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestParseRetryAfterClampsExtremeValues(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"0", 0},
		{"1", time.Second},
		{"999999999999", maximumRetryAfter * time.Second},
		{"-5", 0},
		{"garbage", 0},
	}
	for _, test := range tests {
		if got := parseRetryAfter(test.header, now); got != test.want {
			t.Fatalf("parseRetryAfter(%q)=%s, want %s", test.header, got, test.want)
		}
	}

	farFuture := now.Add(72 * time.Hour)
	if got := parseRetryAfter(farFuture.Format(http.TimeFormat), now); got != maximumRetryAfter*time.Second {
		t.Fatalf("http-date far future Retry-After=%s", got)
	}
	past := now.Add(-time.Minute)
	if got := parseRetryAfter(past.Format(http.TimeFormat), now); got != 0 {
		t.Fatalf("http-date in the past Retry-After=%s", got)
	}
}

func TestTransportErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("connection refused")
	transport := &TransportError{cause: cause}
	if !errors.Is(transport, cause) {
		t.Fatal("TransportError must unwrap its cause")
	}
}