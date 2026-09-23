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

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newServerForIPTest(trustProxy bool) *Server {
	return New(nil, Config{TrustProxy: trustProxy})
}

func TestClientIPTrustsRemoteAddrByDefault(t *testing.T) {
	s := newServerForIPTest(false)
	req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
	req.RemoteAddr = "203.0.113.7:51234"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")

	if got := s.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("must prefer RemoteAddr when TrustProxy=false, got %q", got)
	}
}

func TestClientIPTrustsForwardedWhenProxyEnabled(t *testing.T) {
	s := newServerForIPTest(true)
	req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
	req.RemoteAddr = "10.0.0.1:51234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")

	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("must take first X-Forwarded-For value, got %q", got)
	}
}

func TestClientIPFallsBackToRemoteAddrWhenNoForwarded(t *testing.T) {
	s := newServerForIPTest(true)
	req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
	req.RemoteAddr = "198.51.100.5:51234"

	if got := s.clientIP(req); got != "198.51.100.5" {
		t.Fatalf("must fall back to RemoteAddr when no X-Forwarded-For, got %q", got)
	}
}