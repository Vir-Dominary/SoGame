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

// TestMigrateRoomAPIURLDeprecated 验证废弃 Room API 入口被迁移为"跟随内置默认"
// （置空，经 omitempty 不落盘），含大小写、尾随斜杠、首尾空白等归一化变体。
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
		cfg := &Config{RoomAPIURL: value}
		if !MigrateDeprecatedEndpoints(cfg) {
			t.Errorf("MigrateDeprecatedEndpoints(%q): expected changed=true", value)
			continue
		}
		if cfg.RoomAPIURL != "" {
			t.Errorf("MigrateDeprecatedEndpoints(%q): got %q, want empty (follow built-in default)", value, cfg.RoomAPIURL)
		}
	}
}

// TestMigrateRoomAPIURLIdempotent 验证空值与当前默认值不被改写：
// 空 = 跟随默认，当前默认值不在废弃名单中，两者都应原样保留。
func TestMigrateRoomAPIURLIdempotent(t *testing.T) {
	for _, value := range []string{"", DefaultRoomAPIURL} {
		cfg := &Config{RoomAPIURL: value}
		if MigrateDeprecatedEndpoints(cfg) {
			t.Errorf("value %q must not be rewritten", value)
		}
		if cfg.RoomAPIURL != value {
			t.Errorf("value %q changed to %q", value, cfg.RoomAPIURL)
		}
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
	if cfg.Supernode != "" {
		t.Errorf("supernode = %q, want empty (follow built-in default)", cfg.Supernode)
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
	// 隔离用户配置目录：Windows 读 APPDATA，类 Unix 读 XDG_CONFIG_HOME/HOME，
	// 防止测试触达真实配置目录（包内加密器已惰性化，本测试进程不做任何真实 IO）。
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	configDir := filepath.Join(dir, "SoGame")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	yaml := "meta:\n  app: SoGame\n  author: virdominary\n  version: \"2.0\"\n" +
		"node_name: test-node\ncommunity: community-abcd1234\nkey: \"\"\n" +
		"supernode: 8.148.244.159:10090\nip: 10.10.10.10\nmode: express\n" +
		"room_api_url: http://123.56.254.224\nexpress_nickname: tester\n"
	configFile := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configFile, []byte(yaml), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if cfg.RoomAPIURL != "" {
		t.Fatalf("room_api_url = %q, want empty (migrated to follow built-in default)", cfg.RoomAPIURL)
	}

	// 迁移结果必须已落盘且经 omitempty 不再携带 room_api_url 字段（穿透语义），
	// 再次加载直接读到空值；.bak 留存旧值可回滚。
	persisted, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if strings.Contains(string(persisted), "room_api_url") {
		t.Errorf("persisted config should omit room_api_url after migration, got:\n%s", persisted)
	}
	reloaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.RoomAPIURL != "" {
		t.Errorf("reloaded room_api_url = %q, want empty", reloaded.RoomAPIURL)
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
