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

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestLogger 构造一个写临时文件的 Logger，不触碰全局 globalLogger。
func newTestLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "test.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &Logger{logFile: f, logPath: logPath, minLevel: DEBUG}, logPath
}

// 安全红线回归：房间码/令牌/PAT 经日志写盘点时必须已被脱敏，
// 脱敏由 logger 机制保证而非调用点自觉。
func TestLogWriteRedactsSecrets(t *testing.T) {
	l, logPath := newTestLogger(t)

	l.log(INFO, `heartbeat failed: Post "https://legengen.top/rooms/ABCD-1234-WXYZ/heartbeat": dial tcp timeout`)
	l.log(WARN, "owner_token=owt-8f3k2j9d0s1a rejected")
	l.log(ERROR, "setup_key: 2D989281-59FE-4762-874D-9E053D7E25C3 expired")
	l.log(INFO, "pat=nbp_secretvalue123")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	for _, secret := range []string{
		"ABCD-1234-WXYZ",
		"owt-8f3k2j9d0s1a",
		"2D989281-59FE-4762-874D-9E053D7E25C3",
		"nbp_secretvalue123",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("日志落盘内容仍含敏感值 %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("日志落盘内容缺少脱敏标记:\n%s", got)
	}
}

func TestLogWriteKeepsNormalMessage(t *testing.T) {
	l, logPath := newTestLogger(t)
	l.log(INFO, "room created, 2 members online")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "room created, 2 members online") {
		t.Fatalf("普通日志内容不应被破坏: %s", content)
	}
}
