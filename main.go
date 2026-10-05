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
	logPath := filepath.Join(os.TempDir(), "sogame-update-apply.log")
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
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
// 关键文件（criticalFiles）任一失败即返回错误；其余文件失败仅记录告警。
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
		if err := os.WriteFile(dst, data, 0755); err != nil {
			if criticalFiles[entry.Name()] {
				return fmt.Errorf("写入关键文件 %s 失败: %w", entry.Name(), err)
			}
			log.Printf("warning: write %s failed: %v", entry.Name(), err)
		}
	}
	return nil
}
