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
	"embed"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"sogame/internal/config"
	"sogame/internal/logger"
	"sogame/internal/observability"
	"sogame/internal/updater"
	webui "sogame/internal/webui"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	updateApply := flag.String("update-apply", "", "apply update from the given directory and exit")
	flag.Parse()

	if *updateApply != "" {
		runUpdateApply(*updateApply)
		return
	}

	logger.SetAppInfo(config.AppName, config.AppVersion, config.AppAuthor, config.AppURL)
	if err := logger.Init(); err != nil {
		log.Printf("warning: logger init failed: %v", err)
	}
	defer logger.Close()

	// 红线机制化：logger 包之外的裸 slog 调用（如 nbdaemon 的 slog.Warn）
	// 统一纳入脱敏 handler，不留无脱敏的日志通道。
	slog.SetDefault(slog.New(observability.NewRedactingHandler(os.Stderr, slog.LevelInfo)))

	// 注入热更新 manifest 验签公钥（内置常量；空值=未启用验签）。
	if err := updater.SetManifestPublicKey(config.UpdateManifestPublicKey); err != nil {
		log.Printf("warning: invalid update manifest public key: %v", err)
	}

	app := webui.NewApp()

	err := wails.Run(&options.App{
		Title:  config.AppName,
		Width:  400,
		Height: 780,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.Startup,
		OnShutdown: app.Shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "sogame-unique-id",
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}

// releaseExeName 是发布 zip 中主程序的固定文件名（见 scripts/publish-update.ps1）。
const releaseExeName = "SoGame.exe"

// criticalFiles 为更新必须落盘成功的文件：任一失败即中止更新且不启动新版本，
// 避免留下新旧混合的安装目录。MSI 等可选文件失败仅告警。
var criticalFiles = map[string]bool{
	"SoGame.exe":        true,
	"sogame-helper.exe": true,
	"edge.exe":          true,
}

// runUpdateApply 以更新应用子进程模式运行：把解压目录（srcDir，即 os.Args[0]
// 所在目录）中的新版本文件复制到安装目录 targetDir，然后启动新版本。
// 由 PerformUpdate 以解压目录中的 SoGame.exe 启动，因此 os.Args[0] 天然指向源目录。
func runUpdateApply(targetDir string) {
	// 子进程以 windowsgui 构建，无控制台；把结果落到临时日志文件便于排查。
	// 安全：随机文件名 + CreateTemp 的 O_EXCL 语义——固定路径在 %TEMP% 下
	// 可被同用户进程预置 symlink 指向任意文件，被本提权子进程截断覆盖。
	logFile, err := os.CreateTemp(os.TempDir(), "sogame-update-apply-*.log")
	if err == nil {
		defer logFile.Close()
		log.SetOutput(logFile)
	}

	srcDir := filepath.Dir(os.Args[0])
	if abs, err := filepath.Abs(srcDir); err == nil {
		srcDir = abs
	}
	if abs, err := filepath.Abs(targetDir); err == nil {
		targetDir = abs
	}
	// 防御：源目录与目标目录相同意味着调用方退化回旧逻辑（复制到自己），直接拒绝。
	if strings.EqualFold(srcDir, targetDir) {
		log.Fatalf("update-apply: source directory equals target directory (%s), refusing to continue", srcDir)
	}
	// 防御：targetDir 必须是"当前可执行文件同名 exe 所在的安装目录"的
	// 合法目标——本模式把源目录全部平铺文件复制过去并可被执行，缺少约束
	// 时任何进程都能借它污染用户可写的其他程序目录。判据：targetDir 下
	// 必须已存在与当前 exe 同名的可执行文件（即一个已有的 SoGame 安装）。
	if _, err := os.Stat(filepath.Join(targetDir, filepath.Base(os.Args[0]))); err != nil {
		log.Fatalf("update-apply: target directory %s is not an existing SoGame installation: %v", targetDir, err)
	}

	// 等待旧主进程退出：运行中的 exe 在 Windows 上持有文件锁，不可写。
	exeName := filepath.Base(os.Args[0])
	targetExe := filepath.Join(targetDir, exeName)
	if err := waitExecutableUnlocked(targetExe, 60*time.Second); err != nil {
		log.Fatalf("update-apply: %v", err)
	}

	if err := applyUpdateFiles(srcDir, targetDir); err != nil {
		log.Fatalf("update-apply: %v", err)
	}

	cmd := exec.Command(filepath.Join(targetDir, exeName))
	cmd.Dir = targetDir
	if err := cmd.Start(); err != nil {
		log.Fatalf("update-apply: start new version failed: %v", err)
	}
	log.Printf("update-apply: update applied, new version started")
	os.Exit(0)
}

// waitExecutableUnlocked 轮询等待目标 exe 可写（即旧进程已退出），带总超时。
func waitExecutableUnlocked(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			f.Close()
			return nil
		}
		if os.IsNotExist(err) {
			// 目标 exe 不存在（例如首次布局异常）时无需等待，复制会创建它。
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待旧版本退出超时: %s 仍被占用", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// applyUpdateFiles 把 srcDir 中的平铺文件复制到 targetDir。
// 写入经"临时文件 + 替换"完成：直接截断写目标 exe 时崩溃/断电会在
// 安装目录留下半截 SoGame.exe。关键文件（criticalFiles）任一失败即
// 返回错误；其余文件失败仅记录告警。
func applyUpdateFiles(srcDir, targetDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("读取源目录失败: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			if criticalFiles[entry.Name()] {
				return fmt.Errorf("读取关键文件 %s 失败: %w", entry.Name(), err)
			}
			log.Printf("warning: copy %s failed: %v", entry.Name(), err)
			continue
		}
		if err := writeFileAtomic(dst, data, 0755); err != nil {
			if criticalFiles[entry.Name()] {
				return fmt.Errorf("写入关键文件 %s 失败: %w", entry.Name(), err)
			}
			log.Printf("warning: write %s failed: %v", entry.Name(), err)
		}
	}
	return nil
}

// writeFileAtomic 经"同目录临时文件 + 替换"写文件（Windows 下 os.Rename
// 不能覆盖已存在目标，先删后换；中途失败删除临时文件，目标保持原值）。
func writeFileAtomic(dst string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".update-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	// 目标是目录属于异常状态（安装目录损坏/被占位）：直接报错，
	// 绝不能静默删目录——那会把"占位攻击/误操作"变成数据删除。
	if info, err := os.Lstat(dst); err == nil && info.IsDir() {
		return fmt.Errorf("update target %s is a directory", dst)
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpName, dst)
}
