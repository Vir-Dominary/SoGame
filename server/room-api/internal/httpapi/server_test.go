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
	"strings"
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
	// traefik 追加真实对端 IP 到末尾；首值是客户端可伪造的，必须取最右值。
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")

	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("must take rightmost X-Forwarded-For value, got %q", got)
	}
}

func TestClientIPSingleForwardedValue(t *testing.T) {
	s := newServerForIPTest(true)
	req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
	req.RemoteAddr = "10.0.0.1:51234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	if got := s.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("single X-Forwarded-For value must be used, got %q", got)
	}
}

func TestClientIPSpoofedFirstValueDoesNotBypassLimit(t *testing.T) {
	// 攻击者每次请求随机化 XFF 首值：取最右值时限流桶恒为真实客户端 IP，
	// 伪造首值无法绕过 per-IP 限流。
	s := newServerForIPTest(true)
	seen := make(map[string]bool)
	for _, spoof := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		req := httptest.NewRequest(http.MethodPost, "/rooms", nil)
		req.RemoteAddr = "10.0.0.1:51234"
		req.Header.Set("X-Forwarded-For", spoof+", 203.0.113.9")
		seen[s.clientIP(req)] = true
	}
	if len(seen) != 1 || !seen["203.0.113.9"] {
		t.Fatalf("spoofed XFF first value must not affect client identity, got %v", seen)
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

func TestPathTemplateRedactsRoomCode(t *testing.T) {
	cases := map[string]string{
		"/healthz":                        "/healthz",
		"/rooms/healthz":                  "/rooms/healthz",
		"/rooms":                          "/rooms",
		"/rooms/join":                     "/rooms/join",
		"/rooms/ABCD-1234-WXYZ/peers":     "/rooms/:code/peers",
		"/rooms/ABCD-1234-WXYZ/close":     "/rooms/:code/close",
		"/rooms/ABCD-1234-WXYZ/heartbeat": "/rooms/:code/heartbeat",
		"/rooms/ABCD-1234-WXYZ/disable":   "/rooms/:code/disable",
		"/rooms/ABCD-1234-WXYZ":           "/rooms/*",
		"/rooms/":                         "/rooms/*",
		"/unknown/path":                   "other",
	}
	for path, want := range cases {
		if got := pathTemplate(path); got != want {
			t.Errorf("pathTemplate(%q) = %q, want %q", path, got, want)
		}
	}
	// 任何含房间码形态的路径都不得原样透出。
	code := "ABCD-1234-WXYZ"
	for path := range cases {
		if got := pathTemplate(path); strings.Contains(got, code) {
			t.Errorf("pathTemplate(%q) = %q 泄露了房间码", path, got)
		}
	}
}