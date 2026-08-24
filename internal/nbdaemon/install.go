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

package nbdaemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	releasebuild "sogame/internal/releasebuild"
)

type MSIAction string

const (
	MSIInstall MSIAction = "install"
	MSIRepair  MSIAction = "repair"
)

type ArtifactCheck interface {
	Verify(ctx context.Context, path string, expected releasebuild.WindowsArtifact) error
}

type MSIRunner interface {
	Run(ctx context.Context, action MSIAction, artifactPath, logPath string) error
}

type PrivilegedInstaller struct {
	artifacts ArtifactCheck
	runner    MSIRunner
}

func NewPrivilegedInstaller(artifacts ArtifactCheck, runner MSIRunner) *PrivilegedInstaller {
	return &PrivilegedInstaller{artifacts: artifacts, runner: runner}
}

func (i *PrivilegedInstaller) Execute(ctx context.Context, action MSIAction, artifactPath, logPath string, expected releasebuild.WindowsArtifact) error {
	if action != MSIInstall && action != MSIRepair {
		return ErrUnsupportedAction
	}
	if !filepath.IsAbs(artifactPath) || !strings.EqualFold(filepath.Ext(artifactPath), ".msi") {
		return fmt.Errorf("invalid NetBird MSI path")
	}
	if !filepath.IsAbs(logPath) || !strings.EqualFold(filepath.Ext(logPath), ".log") {
		return fmt.Errorf("invalid installer log path")
	}
	if err := ensureLogDirectory(logPath); err != nil {
		return fmt.Errorf("prepare installer log directory: %w", err)
	}
	if err := i.artifacts.Verify(ctx, artifactPath, expected); err != nil {
		return fmt.Errorf("verify NetBird MSI before %s: %w", action, err)
	}
	// 将已验证的 MSI 复制到专用临时目录,并在副本上二次校验,
	// 收窄"校验与 msiexec 打开之间"的文件替换窗口(TOCTOU)。
	staged, err := stageArtifact(artifactPath)
	if err != nil {
		return fmt.Errorf("stage NetBird MSI: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(filepath.Dir(staged))
	}()
	if err := i.artifacts.Verify(ctx, staged, expected); err != nil {
		return fmt.Errorf("verify staged NetBird MSI before %s: %w", action, err)
	}
	if err := i.runner.Run(ctx, action, staged, logPath); err != nil {
		return fmt.Errorf("run NetBird MSI %s: %w", action, err)
	}
	return nil
}

// stageArtifact 将已验证的 MSI 复制到专用临时目录并返回副本路径。
// 副本在验证后立即由其扣除校验,避免提权安装执行未经验证的字节。
func stageArtifact(sourcePath string) (string, error) {
	file, err := os.Open(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrArtifactMissing
		}
		return "", err
	}
	defer file.Close()
	directory, err := os.MkdirTemp("", "sogame-netbird-*")
	if err != nil {
		return "", err
	}
	staged := filepath.Join(directory, filepath.Base(sourcePath))
	destination, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	_, copyErr := io.Copy(destination, file)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.RemoveAll(directory)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	return staged, nil
}

func ensureLogDirectory(logPath string) error {
	directory := filepath.Dir(logPath)
	if directory == "" || directory == "." {
		return nil
	}
	return os.MkdirAll(directory, 0o700)
}

func BuildMSIArguments(action MSIAction, artifactPath, logPath string) ([]string, error) {
	if action != MSIInstall && action != MSIRepair {
		return nil, ErrUnsupportedAction
	}
	arguments := []string{
		"/i", artifactPath,
		"/quiet", "/qn", "/norestart",
		"/l*v", logPath,
		"AUTOSTART=0",
	}
	if action == MSIRepair {
		arguments = append(arguments, "REINSTALL=ALL", "REINSTALLMODE=vomus")
	}
	return arguments, nil
}
