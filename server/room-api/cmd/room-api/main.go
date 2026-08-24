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
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sogame/server/room-api/internal/config"
	"sogame/server/room-api/internal/httpapi"
	"sogame/server/room-api/internal/netbird"
	"sogame/server/room-api/internal/rooms"
	"sogame/server/room-api/internal/store"
	"syscall"
	"time"
)

func main() {
	migrateDefault := flag.Bool("disable-default-policy", false, "disable the account-wide Default All-to-All policy and exit")
	healthcheck := flag.Bool("healthcheck", false, "check local configuration and SQLite health, then exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); err != nil {
		log.Fatal(err)
	}
	database, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if *healthcheck {
		if err := database.MustHealthy(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}
	client := netbird.New(cfg.ManagementURL, cfg.PAT)
	service := rooms.New(database, client, rooms.Config{ManagementURL: cfg.ManagementURL, EncryptionKey: cfg.EncryptionKey})
	if *migrateDefault {
		if err := service.DisableDefaultPolicy(context.Background()); err != nil {
			log.Fatal(err)
		}
		fmt.Println("default policy disabled")
		return
	}
	if err := service.Reconcile(context.Background()); err != nil {
		log.Printf("room reconciliation failed: %v", err)
	}

	handler := httpapi.New(service, httpapi.Config{
		AdminToken:           cfg.AdminToken,
		MaxBodyBytes:         cfg.MaxBodyBytes,
		CreateRatePerMinute:  cfg.CreateRatePerMinute,
		JoinRatePerMinute:    cfg.JoinRatePerMinute,
		PeerRatePerMinute:    cfg.PeerRatePerMinute,
		ProvisionConcurrency: cfg.ProvisionConcurrency,
	})
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("room API listening on %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}