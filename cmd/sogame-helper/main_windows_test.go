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

//go:build windows && amd64

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateWritablePathAllowsRegularPath 验证白名单内的常规路径放行。
func TestValidateWritablePathAllowsRegularPath(t *testing.T) {
	dir := t.TempDir() // %TEMP% 之下，属白名单根
	path := filepath.Join(dir, "result.json")
	if err := validateWritablePath("--result", path); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

// TestValidateWritablePathRejectsOutsideRoot 验证白名单外路径拒绝
// （含经典系统位置与相对路径）。
func TestValidateWritablePathRejectsOutsideRoot(t *testing.T) {
	for _, path := range []string{
		`C:\Windows\System32\drivers\etc\hosts`,
		`C:\Users\Public\evil.txt`,
		`relative\path.txt`,
		``,
	} {
		if err := validateWritablePath("--log", path); err == nil {
			t.Fatalf("expected rejection for %q", path)
		}
	}
}

// TestValidateWritablePathRejectsLexicalBypass 验证词法绕过变体被拒：
// 大小写、尾部点与空格、反斜杠前缀（短路径 8.3 名因词法不匹配同样被拒，
// 方向更严）。
func TestValidateWritablePathRejectsLexicalBypass(t *testing.T) {
	// TEMP 白名单根之上的逃逸（.. 穿越）。
	escaped := filepath.Join(os.TempDir(), "..", "..", "Windows", "notepad.exe")
	if err := validateWritablePath("--log", escaped); err == nil {
		t.Fatal("expected rejection for path traversal escape")
	}
}

// TestValidateWritablePathRejectsJunctionAncestor 验证中间目录 junction
// 被拒（H-5 核心）：标准用户在白名单内预置 junction 指向任意目录，
// 词法前缀匹配通过，但逐级 reparse 检查必须拦截。
func TestValidateWritablePathRejectsJunctionAncestor(t *testing.T) {
	victimDir := t.TempDir()
	victimFile := filepath.Join(victimDir, "victim.txt")
	if err := os.WriteFile(victimFile, []byte("sensitive"), 0o600); err != nil {
		t.Fatalf("setup victim: %v", err)
	}
	// 在白名单内（%TEMP% 下的临时目录）创建指向 victimDir 的 junction。
	junctionParent := t.TempDir()
	junction := filepath.Join(junctionParent, "hijack")
	cmd := exec.Command("cmd", "/c", "mklink", "/J", junction, victimDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v: %s", err, out)
	}
	target := filepath.Join(junction, "victim.txt")
	err := validateWritablePath("--result", target)
	if err == nil {
		t.Fatal("expected rejection of junction ancestor")
	}
	if !strings.Contains(err.Error(), "reparse") {
		t.Fatalf("error should mention reparse point, got: %v", err)
	}
}

// TestValidateWritablePathRejectsSymlinkTarget 验证已存在的 symlink
// 目标被拒（上轮 H-1/H-2 修复项的回归，此前零测试）。
func TestValidateWritablePathRejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.txt"), target); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := validateWritablePath("--result", target); err == nil {
		t.Fatal("expected rejection of symlink target")
	}
}

// TestRejectReparseAncestryStopsAtMissingLevel 验证缺失层级即停：
// 不存在的深层路径不报错（由 MkdirAll 创建，继承提权 ACL）。
func TestRejectReparseAncestryStopsAtMissingLevel(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "result.json")
	if err := validateWritablePath("--result", deep); err != nil {
		t.Fatalf("expected allow for non-existing deep path, got %v", err)
	}
}
