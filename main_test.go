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

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyUpdateFilesCopiesAll(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := t.TempDir()
	files := map[string]string{
		"SoGame.exe":        "new-main",
		"sogame-helper.exe": "new-helper",
		"edge.exe":          "new-edge",
		"optional.msi":      "new-msi",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := applyUpdateFiles(srcDir, targetDir); err != nil {
		t.Fatalf("applyUpdateFiles: %v", err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(targetDir, name))
		if err != nil {
			t.Fatalf("读取 %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s 内容 = %q, 期望 %q", name, got, want)
		}
	}
}

func TestApplyUpdateFilesCriticalFailureAborts(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := t.TempDir()
	// 关键文件缺失（源目录没有 SoGame.exe 不会报错——ReadDir 只遍历存在的文件；
	// 这里制造写入失败：把目标路径占位为目录）。
	if err := os.WriteFile(filepath.Join(srcDir, "SoGame.exe"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetDir, "SoGame.exe"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := applyUpdateFiles(srcDir, targetDir); err == nil {
		t.Fatal("关键文件写入失败必须返回错误")
	}
}

func TestApplyUpdateFilesSkipsDirectories(t *testing.T) {
	srcDir := t.TempDir()
	targetDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "SoGame.exe"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := applyUpdateFiles(srcDir, targetDir); err != nil {
		t.Fatalf("applyUpdateFiles: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "subdir")); !os.IsNotExist(err) {
		t.Fatal("子目录不应被复制")
	}
}

func TestWaitExecutableUnlockedMissingFile(t *testing.T) {
	// 目标不存在时无需等待，立即返回。
	missing := filepath.Join(t.TempDir(), "not-exist.exe")
	if err := waitExecutableUnlocked(missing, time.Second); err != nil {
		t.Fatalf("目标不存在应立即返回 nil: %v", err)
	}
}

func TestWaitExecutableUnlockedWritableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "free.exe")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := waitExecutableUnlocked(path, time.Second); err != nil {
		t.Fatalf("可写文件应立即返回 nil: %v", err)
	}
}
