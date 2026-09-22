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
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

func runUpdateApply(targetDir string) {
	srcDir := filepath.Dir(os.Args[0])
	if abs, err := filepath.Abs(srcDir); err == nil {
		srcDir = abs
	}
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	exeName := filepath.Base(os.Args[0])
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("warning: copy %s failed: %v", entry.Name(), err)
			continue
		}
		if err := os.WriteFile(dst, data, 0755); err != nil {
			log.Printf("warning: write %s failed: %v", entry.Name(), err)
		}
	}
	cmd := exec.Command(filepath.Join(targetDir, exeName))
	cmd.Dir = targetDir
	cmd.Start()
	os.Exit(0)
}
