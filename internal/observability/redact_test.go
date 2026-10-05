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

package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestRedactingHandlerRemovesCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))
	setupKey := "2D989281-59FE-4762-874D-9E053D7E25C3"
	roomCode := "7X4K-329B-YY95"

	logger.ErrorContext(context.Background(), "join failed room_code="+roomCode,
		"setup_key", setupKey,
		"upstream", "Authorization: Bearer-secret room="+roomCode,
	)

	got := output.String()
	for _, secret := range []string{setupKey, roomCode, "Bearer-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log contains secret %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %s", got)
	}
}

func TestAnonymizeRemovesNetworkAndPeerIdentifiers(t *testing.T) {
	value := "peer=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef ip=100.115.10.21 host=legengen.top url=https://legengen.top/api private=-----BEGIN PRIVATE KEY-----secret-----END PRIVATE KEY-----"
	got := Anonymize(value)
	for _, secret := range []string{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "100.115.10.21", "legengen.top", "BEGIN PRIVATE KEY", "secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("anonymized value contains %q: %s", secret, got)
		}
	}
	for _, marker := range []string{"[PEER_ID]", "[IP]", "[HOST]", "[PRIVATE_KEY]"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("anonymized value lacks %s: %s", marker, got)
		}
	}
}

func TestRedactRemovesOwnerTokenAndPAT(t *testing.T) {
	cases := []string{
		"owner_token=owt-8f3k2j9d0s1a",
		"owner-token: owt-8f3k2j9d0s1a",
		"Owner Token owt-8f3k2j9d0s1a",
		"pat=nbp_secretvalue123",
		"PAT: nbp_secretvalue123",
		"admin_token=adm-9x8y7z",
	}
	for _, c := range cases {
		got := Redact(c)
		for _, secret := range []string{"owt-8f3k2j9d0s1a", "nbp_secretvalue123", "adm-9x8y7z"} {
			if strings.Contains(got, secret) {
				t.Errorf("Redact(%q) = %q, 仍含敏感值 %q", c, got, secret)
			}
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("Redact(%q) = %q, 缺少脱敏标记", c, got)
		}
	}
}

func TestRedactRemovesRoomCodeInURLText(t *testing.T) {
	// *url.Error 文本形态：日志/错误拼接中最常见的房间码泄漏通道。
	value := `Post "https://legengen.top/rooms/ABCD-1234-WXYZ/heartbeat": dial tcp 1.2.3.4:443: i/o timeout`
	got := Redact(value)
	if strings.Contains(got, "ABCD-1234-WXYZ") {
		t.Fatalf("URL 文本中的房间码未脱敏: %s", got)
	}
}
