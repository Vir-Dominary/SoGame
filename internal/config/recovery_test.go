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
	"sync"
	"testing"
)

// isolateConfigDir 把 os.UserConfigDir 重定向到测试临时目录，返回其根，
// 并重置包级加密器缓存（密钥文件路径随 UserConfigDir 变化，旧缓存必然失效）。
func isolateConfigDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	appData := filepath.Join(root, "AppData")
	if err := os.MkdirAll(appData, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", appData)
	resetEncryptorCache()
	return root
}

// resetEncryptorCache 重置包级加密器缓存（同包测试特权；生产代码不得使用）。
func resetEncryptorCache() {
	encryptor = nil
	encryptorOnce = sync.Once{}
}

// TestLoadDecryptFailureClearsKeyNotConfig（H-8 回归）：
// 密钥文件丢失后加密器自动重建新密钥，落盘密文解密必然失败。
// 修复前：密文残留参与 Validate（长度超限）→ .bak（同为密文，同样失败）
// → DefaultConfig 静默整体重置，房间身份丢失。
// 修复后：Key 置空，其余配置原样保留。
func TestLoadDecryptFailureClearsKeyNotConfig(t *testing.T) {
	root := isolateConfigDir(t)

	cfg := DefaultConfig()
	cfg.Community = "audit-community"
	cfg.Key = "0123456789abcdef0123456789abcdef"
	if err := Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 模拟密钥丢失（重装系统等价场景）：删除密钥文件并重置加密器
	// 缓存——下次 getEncryptor 会自动生成新密钥，旧密文必然解不开。
	if err := os.Remove(filepath.Join(root, "AppData", "SoGame", ".key")); err != nil {
		t.Fatalf("remove key file: %v", err)
	}
	resetEncryptorCache()

	loaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate after key loss: %v", err)
	}
	if loaded.Key != "" {
		t.Fatalf("key must be cleared on decrypt failure, got %q", loaded.Key)
	}
	if loaded.Community != "audit-community" {
		t.Fatalf("community must survive key decrypt failure, got %q", loaded.Community)
	}
}

// TestLoadMissingMainFileRestoresFromBackup（M-W3 回归）：
// Save 的 Remove+Rename 两步窗口崩溃后 config.yaml 缺失、.bak 完好，
// LoadOrCreate 必须先恢复备份而不是直接随机化 community。
func TestLoadMissingMainFileRestoresFromBackup(t *testing.T) {
	isolateConfigDir(t)

	cfg := DefaultConfig()
	cfg.Community = "backup-survivor"
	// 第一次 Save 创建主文件；第二次 Save 才会生成 .bak（备份仅在
	// 主文件已存在时创建）。
	if err := Save(cfg); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("save#2: %v", err)
	}

	mainPath, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mainPath + ".bak"); err != nil {
		t.Fatalf("backup must exist after second Save: %v", err)
	}
	if err := os.Remove(mainPath); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate with missing main file: %v", err)
	}
	if loaded.Community != "backup-survivor" {
		t.Fatalf("community must be restored from backup, got %q (randomized?)", loaded.Community)
	}
}

// TestLoadCorruptedYamlRestoresFromBackup（.bak 主路径回归）。
func TestLoadCorruptedYamlRestoresFromBackup(t *testing.T) {
	isolateConfigDir(t)

	cfg := DefaultConfig()
	cfg.Community = "yaml-survivor"
	if err := Save(cfg); err != nil {
		t.Fatalf("save#1: %v", err)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("save#2: %v", err)
	}
	mainPath, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte("{{{not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadOrCreate()
	if err != nil {
		t.Fatalf("LoadOrCreate with corrupted yaml: %v", err)
	}
	if !strings.Contains(loaded.Community, "survivor") {
		t.Fatalf("community must be restored from backup, got %q", loaded.Community)
	}
}
