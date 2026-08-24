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

//go:build windows

package nbdaemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	releasebuild "sogame/internal/releasebuild"
)

const (
	elevationResultPollInterval = 300 * time.Millisecond
	elevationResultTimeout      = 10 * time.Minute
)

// prepareElevation 校验提权辅助程序路径本身的可信性后才发起提权:
//   - 必须是绝对路径下的 sogame-helper.exe 常规文件
//   - 拒绝符号链接与 reparse point(junction),防止重定向到恶意副本
//   - 若发布元数据嵌入了 HelperSHA256,则进一步校验文件哈希
func prepareElevation(helperPath string) error {
	if !filepath.IsAbs(helperPath) || !strings.EqualFold(filepath.Base(helperPath), "sogame-helper.exe") {
		return fmt.Errorf("invalid elevated helper path")
	}
	info, err := os.Lstat(helperPath)
	if err != nil {
		return fmt.Errorf("inspect elevated helper: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("elevated helper must be a regular file")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("elevated helper must not be a symbolic link")
	}
	pathUTF16, err := windows.UTF16PtrFromString(helperPath)
	if err != nil {
		return fmt.Errorf("encode elevated helper path: %w", err)
	}
	attributes, err := windows.GetFileAttributes(pathUTF16)
	if err != nil {
		return fmt.Errorf("read elevated helper attributes: %w", err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("elevated helper must not be a reparse point")
	}
	metadata, err := releasebuild.Load()
	if err != nil {
		return err
	}
	if metadata.HelperSHA256 != "" {
		if err := verifyHelperDigest(helperPath, metadata.HelperSHA256); err != nil {
			return err
		}
	}
	return nil
}

func verifyHelperDigest(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open elevated helper for digest: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := file.WriteTo(hash); err != nil {
		return fmt.Errorf("hash elevated helper: %w", err)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("elevated helper digest mismatch")
	}
	return nil
}

// launchElevated 通过 UAC 启动提权辅助进程,等待其结果文件后返回
// 辅助进程的真实执行结果(成功/失败原因)。
func launchElevation(helperPath string, arguments []string, resultPath string) error {
	if err := os.Remove(resultPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reset elevation result file: %w", err)
	}
	escaped := make([]string, len(arguments))
	for index, argument := range arguments {
		escaped[index] = syscall.EscapeArg(argument)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(helperPath)
	parameters, _ := windows.UTF16PtrFromString(strings.Join(escaped, " "))
	directory, _ := windows.UTF16PtrFromString(filepath.Dir(helperPath))
	if err := windows.ShellExecute(0, verb, file, parameters, directory, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return ErrElevationCancelled
		}
		return fmt.Errorf("request elevated helper: %w", err)
	}
	return awaitElevationResult(context.Background(), resultPath)
}

func RequestInstallerElevation(helperPath string, action MSIAction, artifactPath, logPath, resultPath string) error {
	if action != MSIInstall && action != MSIRepair {
		return ErrUnsupportedAction
	}
	if !filepath.IsAbs(artifactPath) || !filepath.IsAbs(logPath) || !filepath.IsAbs(resultPath) {
		return fmt.Errorf("elevated helper requires absolute paths")
	}
	if err := prepareElevation(helperPath); err != nil {
		return err
	}
	arguments := []string{
		"--action", string(action),
		"--artifact", artifactPath,
		"--log", logPath,
		"--result", resultPath,
	}
	return launchElevation(helperPath, arguments, resultPath)
}

func RequestDaemonRemovalElevation(helperPath string, confirmed bool, logPath, resultPath string) error {
	if !confirmed {
		return ErrRemovalNotConfirmed
	}
	if !filepath.IsAbs(logPath) || !filepath.IsAbs(resultPath) {
		return fmt.Errorf("elevated helper requires absolute paths")
	}
	if err := prepareElevation(helperPath); err != nil {
		return err
	}
	arguments := []string{
		"--action", string(MSIRemove),
		"--log", logPath,
		"--result", resultPath,
	}
	return launchElevation(helperPath, arguments, resultPath)
}

// awaitElevationResult 轮询提权辅助进程写入的结果文件,直到其出现、
// 上下文取消或超时。返回 helper 报告的错误(如果有)。
func awaitElevationResult(ctx context.Context, resultPath string) error {
	timer := time.NewTimer(elevationResultTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(elevationResultPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrElevationTimedOut
		case <-ticker.C:
			payload, err := os.ReadFile(resultPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read elevation result: %w", err)
			}
			return parseElevationResult(payload)
		}
	}
}

func parseElevationResult(payload []byte) error {
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return fmt.Errorf("elevated helper returned an unreadable result: %w", err)
	}
	if !result.Success {
		if result.Error == "" {
			return fmt.Errorf("elevated helper reported a failure")
		}
		return fmt.Errorf("elevated helper failed: %s", result.Error)
	}
	return nil
}