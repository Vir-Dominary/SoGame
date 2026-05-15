package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"netjoin/internal/config"
	"netjoin/internal/logger"
	webui "netjoin/internal/webui"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger.SetAppInfo(config.AppName, config.AppVersion, config.AppAuthor, config.AppURL)
	if err := logger.Init(); err != nil {
		log.Printf("warning: logger init failed: %v", err)
	}
	defer logger.Close()

	app := webui.NewApp()

	err := wails.Run(&options.App{
		Title:  config.AppName,
		Width:  400,
		Height: 620,
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
