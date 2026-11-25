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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"sogame/internal/nbdaemon"
	releasebuild "sogame/internal/releasebuild"
)

// sogame-helper 是一个提权辅助程序,由 SoGame 主程序通过 ShellExecute("runas")
// 启动,用于以管理员权限安装/修复/移除官方 NetBird MSI。
//
// 用法：
//   sogame-helper.exe --action install  --artifact <msi路径> --log <日志路径> --result <结果路径>
//   sogame-helper.exe --action repair   --artifact <msi路径> --log <日志路径> --result <结果路径>
//   sogame-helper.exe --action remove                      --log <日志路径> --result <结果路径>
//
// 无论成功与否,都会向 --result 指向的 JSON 文件写入执行结果,
// 供提权发起方读取真实结果(SW_HIDE 下控制台不可见)。
func main() {
	actionFlag := flag.String("action", "", "install, repair, or remove")
	artifactPath := flag.String("artifact", "", "absolute path to the verified official NetBird MSI")
	logPath := flag.String("log", "", "absolute path to the local MSI log")
	resultPath := flag.String("result", "", "absolute path to the JSON result file")
	flag.Parse()

	writeResult := func(runErr error) {
		if *resultPath == "" {
			return
		}
		if err := validateWritablePath("--result", *resultPath); err != nil {
			fmt.Fprintln(os.Stderr, "warn: refusing to write result file:", err)
			return
		}
		reportResult(*resultPath, runErr)
	}

	runErr := run(*actionFlag, *artifactPath, *logPath)
	writeResult(runErr)
	if runErr != nil {
		fail(runErr)
	}
}

func run(actionValue, artifactPath, logPath string) error {
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flag.Args())
	}
	if err := validateWritablePath("--log", logPath); err != nil {
		return err
	}
	action := nbdaemon.MSIAction(actionValue)
	if action != nbdaemon.MSIInstall && action != nbdaemon.MSIRepair && action != nbdaemon.MSIRemove {
		return nbdaemon.ErrUnsupportedAction
	}
	metadata, err := releasebuild.Load()
	if err != nil {
		return err
	}
	if action == nbdaemon.MSIInstall || action == nbdaemon.MSIRepair {
		if artifactPath == "" || !filepath.IsAbs(artifactPath) {
			return fmt.Errorf("--artifact must be an absolute MSI path")
		}
	}
	if err := ensureLogDirectory(logPath); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runner := nbdaemon.NewWindowsMSIRunner()
	if action == nbdaemon.MSIRemove {
		if err := nbdaemon.NewDaemonRemover(runner).Remove(ctx, true, metadata.WindowsX64.Install.ProductCode, logPath); err != nil {
			return err
		}
		return nil
	}
	installer := nbdaemon.NewPrivilegedInstaller(
		nbdaemon.NewArtifactVerifier(nbdaemon.WindowsSignatureVerifier{}),
		runner,
	)
	if err := installer.Execute(ctx, action, artifactPath, logPath, metadata.WindowsX64); err != nil {
		return err
	}
	// 安装/修复成功：NetBird 仅作为隐藏守护进程使用。
	// 清理桌面/开始菜单快捷方式与登录自启动项；清理失败只记录警告，不阻塞主流程。
	if err := nbdaemon.HideNetBirdSurfaces(); err != nil {
		fmt.Fprintln(os.Stderr, "warn: hide NetBird surfaces failed:", err)
	}
	// 确保守护进程服务已启动（MSI 已尝试启动，这里兜底），
	// 否则安装"成功"但守护进程不可用。
	return nbdaemon.EnsureNetBirdServiceRunning(ctx)
}

func ensureLogDirectory(logPath string) error {
	return os.MkdirAll(filepath.Dir(logPath), 0o700)
}

// validateWritablePath 约束提权后写文件的目标路径，防止 helper 被当成
// "管理员任意文件写"原语滥用：
//   - 必须是绝对路径；
//   - 必须位于 SoGame 约定的数据目录之下（%LOCALAPPDATA%\SoGame、
//     %ProgramData%\SoGame 或系统临时目录）；
//   - 从白名单根到目标的每一级**已存在**父目录不得是 reparse point
//     （junction/mount point 创建无需特权，标准用户预置
//     `mklink /J %ProgramData%\SoGame <任意目录>` 即可把提权写入
//     重定向出白名单——词法前缀匹配防不住，必须逐级检查）；
//   - 目标若已存在，必须是常规文件且不得是 reparse point。
func validateWritablePath(flagName, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", flagName)
	}
	clean := filepath.Clean(path)
	root := ""
	for _, candidate := range writableRoots() {
		if candidate == "" {
			continue
		}
		rel, err := filepath.Rel(candidate, clean)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			root = candidate
			break
		}
	}
	if root == "" {
		return fmt.Errorf("%s path is outside the allowed SoGame data directories", flagName)
	}
	if err := rejectReparseAncestry(flagName, root, clean); err != nil {
		return err
	}
	return rejectReparsePoint(flagName, clean)
}

// rejectReparseAncestry 检查从白名单根到目标路径的每一级已存在父目录
// 都不是 reparse point。遇到不存在的层级即停：剩余层级由
// ensureLogDirectory/MkdirAll 以提权身份创建，继承安全 ACL，标准用户
// 无权事后替换。
func rejectReparseAncestry(flagName, root, clean string) error {
	rel := strings.TrimPrefix(strings.TrimPrefix(clean, root), string(filepath.Separator))
	if rel == "" {
		return nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := root
	// 最后一段是目标文件本身，由 rejectReparsePoint 单独检查。
	for i := 0; i < len(parts)-1; i++ {
		current = filepath.Join(current, parts[i])
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s ancestor: %w", flagName, err)
		}
		pathUTF16, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return fmt.Errorf("encode %s ancestor path: %w", flagName, err)
		}
		attributes, err := windows.GetFileAttributes(pathUTF16)
		if err != nil {
			return fmt.Errorf("read %s ancestor attributes: %w", flagName, err)
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("%s ancestor %s must not be a reparse point", flagName, current)
		}
		// Go 的 os.Lstat 把 junction 识别为 symlink 而非目录，上面的
		// reparse 检查负责拒绝它们；此处只拒绝"非目录且非 reparse"的
		// 普通文件占位（如攻击者用同名文件挡住目录层级）。
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s ancestor %s is not a directory", flagName, current)
		}
	}
	return nil
}

func writableRoots() []string {
	roots := []string{filepath.Clean(os.TempDir())}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Clean(filepath.Join(local, "SoGame")))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		roots = append(roots, filepath.Clean(filepath.Join(programData, "SoGame")))
	}
	return roots
}

// rejectReparsePoint 在路径已存在时拒绝 reparse point 与非常规文件；
// 不存在的路径由调用方负责在写入前重新检查（见 reportResult）。
func rejectReparsePoint(flagName, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s target: %w", flagName, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s target must be a regular file", flagName)
	}
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode %s path: %w", flagName, err)
	}
	attributes, err := windows.GetFileAttributes(pathUTF16)
	if err != nil {
		return fmt.Errorf("read %s attributes: %w", flagName, err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s target must not be a reparse point", flagName)
	}
	return nil
}

// reportResult 将结果写入调用方指定的 JSON 文件。
// 采用"临时文件 + rename"原子发布，主程序轮询读取时不会撞上写一半的
// 内容而被误报为失败；写入前再次拒绝 reparse point（缩小与主程序侧
// os.Remove 之间的 TOCTOU 窗口）。
func reportResult(path string, runErr error) {
	result := struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}{
		Success: runErr == nil,
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return
	}
	if err := rejectReparsePoint("--result", path); err != nil {
		fmt.Fprintln(os.Stderr, "warn: refusing to write result file:", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".result-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		fmt.Fprintln(os.Stderr, "warn: publish result file failed:", err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}