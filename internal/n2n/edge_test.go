// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors

package n2n

import (
	"strings"
	"testing"

	"sogame/internal/config"
)

func TestBuildArgsContainsAllFlags(t *testing.T) {
	cfg := &config.Config{
		Community: "test-room",
		Key:       "0123456789abcdef0123456789abcdef",
		IP:        "10.10.10.5",
		Supernode: "1.2.3.4:10090",
		MgmtPort:  45678,
	}
	args := BuildArgs(cfg)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-c test-room", "-k 0123456789abcdef0123456789abcdef", "-a 10.10.10.5", "-l 1.2.3.4:10090", "-t 45678"} {
		if !strings.Contains(joined, want) {
			t.Errorf("BuildArgs missing %q: %v", want, args)
		}
	}
}

func TestBuildArgsForLoggingMasksKey(t *testing.T) {
	cfg := &config.Config{
		Community: "test-room",
		Key:       "0123456789abcdef0123456789abcdef",
		IP:        "10.10.10.5",
		Supernode: "1.2.3.4:10090",
		MgmtPort:  45678,
	}
	masked := BuildArgsForLogging(cfg)
	joined := strings.Join(masked, " ")
	if strings.Contains(joined, cfg.Key) {
		t.Fatalf("logging args must not contain the plain key: %v", masked)
	}
	if !strings.Contains(joined, "-k ******") {
		t.Fatalf("key must be masked: %v", masked)
	}
	// 与 BuildArgs 同长：扩展参数（-d/-t 等）不得因脱敏而丢失
	if len(masked) != len(BuildArgs(cfg)) {
		t.Fatalf("masked args length %d != raw args length %d", len(masked), len(BuildArgs(cfg)))
	}
}

func TestIsRegistrationSuccessLine(t *testing.T) {
	positive := []string{
		"[ok] edge <<< ================ >>> supernode",
		"registered with supernode",
		"successfully registered",
		"连接状态: 已成功注册到中心节点",
		"supernode0: OK",
		"[OK] edge >>> supernode",
	}
	for _, line := range positive {
		if !isRegistrationSuccessLine(strings.ToLower(line)) {
			t.Errorf("expected registration success for %q", line)
		}
	}
	negative := []string{
		"connected to supernode",      // 中间状态，不是注册成功
		"edge started",                // 普通启动行
		"ok",                          // 只有 ok 不含 edge/supernode
		"edge_operate error occurred", // 含 error 不得命中
		"received packet from peer",
		"",
	}
	for _, line := range negative {
		if isRegistrationSuccessLine(strings.ToLower(line)) {
			t.Errorf("false positive for %q", line)
		}
	}
}

func TestIsConnectedToSupernodeLine(t *testing.T) {
	if !isConnectedToSupernodeLine("connected to supernode") {
		t.Error("expected connected-to-supernode match")
	}
	if !isConnectedToSupernodeLine("supernode connection established") {
		t.Error("expected established match")
	}
	if isConnectedToSupernodeLine("registered with supernode") {
		t.Error("registered must not match connected predicate")
	}
}
