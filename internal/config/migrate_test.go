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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateRoomAPIURLDeprecated 验证废弃 Room API 入口被迁移为当前默认值，
// 含大小写、尾随斜杠、首尾空白等归一化变体。
func TestMigrateRoomAPIURLDeprecated(t *testing.T) {
	deprecated := []string{
		"http://123.56.254.224",
		"http://123.56.254.224/",
		"HTTP://123.56.254.224",
		"  http://123.56.254.224  ",
		"http://virdy.cn",
		"https://virdy.cn",
		"https://virdy.cn/",
	}
	for _, value := range deprecated {
		// 名单成员恰好等于当前默认值时（名单先于默认值切换发版的过渡期）
		// 走幂等路径，由 TestMigrateRoomAPIURLIdempotent 覆盖
		normalized := strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
		if normalized == strings.ToLower(strings.TrimRight(DefaultRoomAPIURL, "/")) {
			continue
		}
		cfg := &Config{RoomAPIURL: value}
		if !MigrateDeprecatedEndpoints(cfg) {
			t.Errorf("MigrateDeprecatedEndpoints(%q): expected changed=true", value)
			continue
		}
		if cfg.RoomAPIURL != DefaultRoomAPIURL {
			t.Errorf("MigrateDeprecatedEndpoints(%q): got %q, want %q", value, cfg.RoomAPIURL, DefaultRoomAPIURL)
		}
	}
}

// TestMigrateRoomAPIURLIdempotent 验证当前默认值不被改写（即便它在废弃名单中，
// 例如名单先于默认值切换发版的过渡期），保证迁移幂等。
func TestMigrateRoomAPIURLIdempotent(t *testing.T) {
	cfg := &Config{RoomAPIURL: DefaultRoomAPIURL}
	if MigrateDeprecatedEndpoints(cfg) {
		t.Errorf("current default %q must not be rewritten", DefaultRoomAPIURL)
	}
	if cfg.RoomAPIURL != DefaultRoomAPIURL {
		t.Errorf("default value changed to %q", cfg.RoomAPIURL)
	}
}

// TestMigrateRoomAPIURLPreservesCustom 验证用户自定义地址与空值不被迁移。
func TestMigrateRoomAPIURLPreservesCustom(t *testing.T) {
	preserved := []string{
		"", // 空 = 跟随内置默认
		"http://127.0.0.1:9099",
		"https://room-api.example.com",
		"https://legengen.top",
	}
	for _, value := range preserved {
		cfg := &Config{RoomAPIURL: value}
		if MigrateDeprecatedEndpoints(cfg) {
			t.Errorf("MigrateDeprecatedEndpoints(%q): custom value must be preserved", value)
		}
		if cfg.RoomAPIURL != value {
			t.Errorf("MigrateDeprecatedEndpoints(%q): got %q", value, cfg.RoomAPIURL)
		}
	}
}

// TestMigrateSupernode 验证废弃中心节点迁移（名单当前为空，测试注入临时项）。
func TestMigrateSupernode(t *testing.T) {
	deprecatedSupernodes["1.2.3.4:10090"] = true
	defer delete(deprecatedSupernodes, "1.2.3.4:10090")

	cfg := &Config{Supernode: "1.2.3.4:10090"}
	if !MigrateDeprecatedEndpoints(cfg) {
		t.Fatalf("deprecated supernode should be migrated")
	}
	if cfg.Supernode != DefaultSupernode {
		t.Errorf("supernode = %q, want %q", cfg.Supernode, DefaultSupernode)
	}

	// 当前默认节点与普通自定义节点不受影响
	for _, value := range []string{DefaultSupernode, "8.8.8.8:10090", ""} {
		cfg := &Config{Supernode: value}
		if MigrateDeprecatedEndpoints(cfg) {
			t.Errorf("supernode %q must not be migrated", value)
		}
		if cfg.Supernode != value {
			t.Errorf("supernode %q changed to %q", value, cfg.Supernode)
		}
	}
}

// TestMigrateNilConfig 防御：nil 配置不 panic。
func TestMigrateNilConfig(t *testing.T) {
	if MigrateDeprecatedEndpoints(nil) {
		t.Errorf("nil config should report no change")
	}
}

// TestNormalizeSupernodeEmptyList 名单为空时地址原样返回（含 trim）。
func TestNormalizeSupernodeEmptyList(t *testing.T) {
	if got := NormalizeSupernode("  8.148.244.159:10090 "); got != "8.148.244.159:10090" {
		t.Errorf("NormalizeSupernode should trim, got %q", got)
	}
	if got := NormalizeSupernode(""); got != "" {
		t.Errorf("NormalizeSupernode(\"\") should stay empty, got %q", got)
	}
}

// TestLoadOrCreateMigratesDeprecated 端到端：磁盘上的旧配置加载后被迁移并落盘。
func TestLoadOrCreateMigratesDeprecated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir) // Windows 上 os.UserConfigDir 指向 %AppData%

	configDir := filepath.Join(dir, "SoGame")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := "meta:\n  app: SoGame\n  author: virdominary\n  version: \"2.0\"\n" +
		"node_name: test-node\ncommunity: community-abcd1234\nkey: \"\"\n" +
		"supernode: 8.148.244.159:10090\nip: 10.10.10.10\nmode: express\n" +
		"room_api_url: http://123.56.254.224\nexpress_nickname: tester\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if cfg.RoomAPIURL != DefaultRoomAPIURL {
		t.Fatalf("room_api_url = %q, want migrated default %q", cfg.RoomAPIURL, DefaultRoomAPIURL)
	}

	// 迁移结果必须已落盘（再次加载直接读到新值，且 .bak 留存旧值）
	reloaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.RoomAPIURL != DefaultRoomAPIURL {
		t.Errorf("persisted room_api_url = %q, want %q", reloaded.RoomAPIURL, DefaultRoomAPIURL)
	}
	backup, err := os.ReadFile(filepath.Join(configDir, "config.yaml.bak"))
	if err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if !strings.Contains(string(backup), "http://123.56.254.224") {
		t.Errorf("backup should retain the old value for rollback")
	}
}

// TestDefaultConfigLeavesEndpointsEmpty 验证默认值不落盘语义（omitempty 穿透）。
func TestDefaultConfigLeavesEndpointsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RoomAPIURL != "" {
		t.Errorf("DefaultConfig RoomAPIURL = %q, want empty (follow built-in default)", cfg.RoomAPIURL)
	}
	if cfg.Supernode != "" {
		t.Errorf("DefaultConfig Supernode = %q, want empty (follow built-in default)", cfg.Supernode)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config with empty endpoints must pass validation: %v", err)
	}
}
