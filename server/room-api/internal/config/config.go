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

package config

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr                 string
	ManagementURL        string
	PAT                  string
	EncryptionKey        []byte
	DBPath               string
	AdminToken           string
	CreateRatePerMinute  int
	JoinRatePerMinute    int
	PeerRatePerMinute    int
	MaxBodyBytes         int64
	ProvisionConcurrency int
}

func Load() (Config, error) {
	c := Config{
		Addr:                 env("ROOM_API_ADDR", ":8080"),
		ManagementURL:        strings.TrimRight(env("NETBIRD_MANAGEMENT_URL", "https://legengen.top"), "/"),
		PAT:                  os.Getenv("NETBIRD_PAT"),
		DBPath:               env("ROOM_API_DB_PATH", "room-api.db"),
		AdminToken:           os.Getenv("ROOM_API_ADMIN_TOKEN"),
		CreateRatePerMinute:  intEnv("ROOM_API_CREATE_RATE_PER_MINUTE", 5),
		JoinRatePerMinute:    intEnv("ROOM_API_JOIN_RATE_PER_MINUTE", 30),
		PeerRatePerMinute:    intEnv("ROOM_API_PEER_RATE_PER_MINUTE", 60),
		MaxBodyBytes:         int64Env("ROOM_API_MAX_BODY_BYTES", 4096),
		ProvisionConcurrency: intEnv("ROOM_API_PROVISION_CONCURRENCY", 2),
	}
	if c.PAT == "" {
		return Config{}, fmt.Errorf("NETBIRD_PAT is required")
	}
	if c.CreateRatePerMinute < 1 || c.JoinRatePerMinute < 1 || c.PeerRatePerMinute < 1 || c.ProvisionConcurrency < 1 {
		return Config{}, fmt.Errorf("rate limits and provision concurrency must be positive")
	}
	key, err := encryptionKey(os.Getenv("ROOM_API_ENCRYPTION_KEY"))
	if err != nil {
		return Config{}, err
	}
	c.EncryptionKey = key
	return c, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func intEnv(name string, fallback int) int {
	value, err := strconv.Atoi(env(name, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return value
}

func int64Env(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(env(name, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func encryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("ROOM_API_ENCRYPTION_KEY is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	digest := sha256.Sum256([]byte(value))
	return digest[:], nil
}