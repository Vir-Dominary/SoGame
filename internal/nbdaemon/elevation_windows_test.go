// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors

//go:build windows

package nbdaemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseElevationResultIncompleteIsRetryable(t *testing.T) {
	// helper 写入中途的半包 JSON 必须被判定为"未完成"（调用方继续轮询），
	// 而不是把一次成功的安装误报为失败。
	err := parseElevationResult([]byte(`{"success": tru`))
	if !errors.Is(err, errElevationResultIncomplete) {
		t.Fatalf("expected errElevationResultIncomplete for partial payload, got %v", err)
	}
}

func TestParseElevationResultSuccessAndFailure(t *testing.T) {
	if err := parseElevationResult([]byte(`{"success":true}`)); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if err := parseElevationResult([]byte(`{"success":false,"error":"boom"}`)); err == nil {
		t.Fatal("expected failure to surface")
	}
}

func TestReadElevationResultFileRejectsOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	payload := make([]byte, elevationResultMaxBytes+1024)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readElevationResultFile(path); err == nil {
		t.Fatal("expected oversize result to be rejected")
	}
}

func TestReadElevationResultFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := readElevationResultFile(dir); err == nil {
		t.Fatal("expected directory to be rejected")
	}
}

func TestAwaitElevationResultToleratesPartialThenCompleteWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	// 先写半包（模拟旧版 helper 直写中途被读到），随后原子替换为完整结果。
	if err := os.WriteFile(path, []byte(`{"success": tru`), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"success":true}`), 0o600)
	}()
	done := make(chan error, 1)
	go func() { done <- awaitElevationResult(t.Context(), path) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected eventual success, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("awaitElevationResult did not complete after full result published")
	}
}
