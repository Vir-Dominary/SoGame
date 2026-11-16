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
	"log"
	"os"
	"strconv"
	"strings"
	"time"
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
	// RelayEnabled 表示该服务器是否允许房间使用 Relay 中继。
	// 由提供服务的服务器掌握，随房间 enrollment 下发给客户端：
	//   false（默认）→ 纯 P2P 优先，客户端不把中继连接视为已连接
	//   true → 允许中继，客户端 P2P 失败时可使用中继回退
	RelayEnabled bool
	// TrustProxy 表示请求是否经由可信反向代理转发；
	// 为 true 时信任 X-Forwarded-For 用于限流与审计，否则只取 TCP 对端地址。
	TrustProxy bool
	// 房主离线超时：超过该时长未收到房主心跳即自动解散房间。<=0 关闭本机制。
	OwnerOfflineAfter time.Duration
	// 房主看门狗扫描间隔。
	OwnerSweepInterval time.Duration
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
		RelayEnabled:         boolEnv("ROOM_API_RELAY_ENABLED", false),
		TrustProxy:           boolEnv("ROOM_API_TRUST_PROXY", false),
		OwnerOfflineAfter:    durationEnv("ROOM_API_OWNER_OFFLINE_AFTER", 5*time.Minute),
		OwnerSweepInterval:   durationEnv("ROOM_API_OWNER_SWEEP_INTERVAL", time.Minute),
	}
	if c.PAT == "" {
		return Config{}, fmt.Errorf("NETBIRD_PAT is required")
	}
	if c.CreateRatePerMinute < 1 || c.JoinRatePerMinute < 1 || c.PeerRatePerMinute < 1 || c.ProvisionConcurrency < 1 {
		return Config{}, fmt.Errorf("rate limits and provision concurrency must be positive")
	}
	// MaxBodyBytes 下限：过小值会让 ContentLength 预检与 decodeJSON 兜底
	// 行为分裂（一个全拒、一个放行小请求体），属配置错误应显式拒绝。
	if c.MaxBodyBytes < 256 {
		return Config{}, fmt.Errorf("ROOM_API_MAX_BODY_BYTES must be >= 256 (got %d)", c.MaxBodyBytes)
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

// 解析失败必须 WARN：静默回退默认值会让配置拼写错误（如
// ROOM_API_RELAY_ENABLED=ture）无声生效，排障时无从下手。
func intEnv(name string, fallback int) int {
	raw := os.Getenv(name)
	value, err := strconv.Atoi(env(name, strconv.Itoa(fallback)))
	if err != nil {
		log.Printf("config: invalid %s=%q, falling back to %d", name, raw, fallback)
		return fallback
	}
	return value
}

func int64Env(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	value, err := strconv.ParseInt(env(name, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil {
		log.Printf("config: invalid %s=%q, falling back to %d", name, raw, fallback)
		return fallback
	}
	return value
}

// durationEnv 解析时长环境变量（如 "5m"、"30s"）；无法解析时返回 fallback。
func durationEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("config: invalid %s=%q, falling back to %s", name, value, fallback)
		return fallback
	}
	return parsed
}

// boolEnv 解析布尔环境变量；无法解析时返回 fallback。
func boolEnv(name string, fallback bool) bool {
	raw := os.Getenv(name)
	parsed, err := strconv.ParseBool(env(name, strconv.FormatBool(fallback)))
	if err != nil {
		log.Printf("config: invalid %s=%q, falling back to %t", name, raw, fallback)
		return fallback
	}
	return parsed
}

func encryptionKey(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("ROOM_API_ENCRYPTION_KEY is required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		// base64 合法但解码非 32B：说明运维给错了密钥格式（如
		// openssl rand -base64 16），静默降级到 SHA256 派生与预期不符，
		// 显式报错。
		if len(decoded) != 32 {
			return nil, fmt.Errorf("ROOM_API_ENCRYPTION_KEY is valid base64 but decodes to %d bytes, want 32 (generate with: openssl rand -base64 32)", len(decoded))
		}
		return decoded, nil
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	// 其他取值按 SHA256 派生。低强度口令无 KDF 强化（DB 泄露后可字典
	// 爆破），必须使用高熵随机值——README 有同等要求。
	log.Printf("config: ROOM_API_ENCRYPTION_KEY derived via SHA256; prefer base64(32B) from `openssl rand -base64 32`")
	digest := sha256.Sum256([]byte(value))
	return digest[:], nil
}