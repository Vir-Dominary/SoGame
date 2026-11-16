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

func TestRedactingHandlerMasksURLCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))

	logger.ErrorContext(context.Background(), "request failed",
		"endpoint", "https://admin:secret@example.com/room/AAAA-BBBB-CCCC",
	)

	got := output.String()
	for _, secret := range []string{"admin:secret", "AAAA-BBBB-CCCC"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log contains secret %q: %s", secret, got)
		}
	}
}

func TestRedactingHandlerMasksArbitraryValueObjects(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))

	// KindAny 的值必须经过脱敏后再渲染,不能绕过 redactAttr。
	logger.LogAttrs(context.Background(), slog.LevelError, "anything",
		slog.Any("setup_key", 2.5e18),
		slog.Any("room_code", "7X4K-329B-YY95"),
		slog.Any("payload", []byte("7X4K-329B-YY95")),
	)

	got := output.String()
	for _, secret := range []string{"7X4K-329B-YY95", "2.5e+18"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log contains secret %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker: %s", got)
	}
}

func TestRedactMasksURLsWithCredentialsAndRoomCodes(t *testing.T) {
	input := "control plane url=https://user:pwd@legengen.top:8443/panel?room=AAAA-BBBB-CCCC"
	got := Redact(input)
	for _, secret := range []string{"user:pwd", "AAAA-BBBB-CCCC"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted value contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "legengen.top") {
		t.Fatalf("redaction removed the URL host: %s", got)
	}
}

func TestRedactMasksEdgeKeyArgument(t *testing.T) {
	// n2n edge argv 形态：-k 后跟随明文社区密钥，必须脱敏（DEBUG 日志引信回归）。
	key := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	input := "edge args: -c myroom -k " + key + " -a 10.10.10.5"
	got := Redact(input)
	if strings.Contains(got, key) {
		t.Fatalf("edge key leaked after redaction: %s", got)
	}
	if !strings.Contains(got, "-c myroom") {
		t.Fatalf("redaction should preserve non-secret args: %s", got)
	}
}

func TestRedactMasksHex32ClassicKey(t *testing.T) {
	// 经典模式密钥为 hex(16B) 的 32 位十六进制，任何文本形态出现都兜底脱敏。
	key := "0123456789abcdef0123456789abcdef"
	got := Redact("decrypt failed for key " + key)
	if strings.Contains(got, key) {
		t.Fatalf("hex32 key leaked after redaction: %s", got)
	}
}

func TestRedactingHandlerResolvesLogValuer(t *testing.T) {
	// KindLogValuer 不展开会绕过全部脱敏分支（旁路回归）。
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(&output, slog.LevelDebug))

	logger.LogAttrs(context.Background(), slog.LevelError, "valuer",
		slog.Any("wrapped", roomCodeValuer{"AAAA-BBBB-CCCC"}),
	)

	got := output.String()
	if strings.Contains(got, "AAAA-BBBB-CCCC") {
		t.Fatalf("LogValuer bypassed redaction: %s", got)
	}
}

type roomCodeValuer struct{ code string }

func (v roomCodeValuer) LogValue() slog.Value { return slog.StringValue(v.code) }