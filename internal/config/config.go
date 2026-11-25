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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"sogame/internal/logger"
	"sogame/internal/security"

	"gopkg.in/yaml.v3"
)

type Meta struct {
	App     string `yaml:"app"`
	Author  string `yaml:"author"`
	Version string `yaml:"version"`
}

type Config struct {
	Meta      Meta   `yaml:"meta"`
	NodeName  string `yaml:"node_name"`
	Community string `yaml:"community"`
	Key       string `yaml:"key"`
	Supernode string `yaml:"supernode,omitempty"` // 空 = 跟随内置默认 DefaultSupernode
	IP        string `yaml:"ip"`
	MgmtPort  int    `yaml:"-"` // n2n edge 管理端口（运行时生成，不持久化）

	// 联机模式：classic（n2n+tap）/ express（netbird+wireguard）
	// 空值视为 classic 以保持向后兼容
	Mode string `yaml:"mode"`

	// 极速模式（express）配置：基于 netbird 实现
	RoomAPIURL     string `yaml:"room_api_url,omitempty"` // Room API 服务地址；空 = 跟随内置默认 DefaultRoomAPIURL
	ExpressNickname string `yaml:"express_nickname"`      // 极速模式下的展示昵称
}

// encryptor 全局加密器（惰性初始化，见 getEncryptor）。
var (
	encryptor     *security.Encryptor
	encryptorOnce sync.Once
)

// getEncryptor 返回全局加密器，首次实际加解密时才初始化。
// 原为包级 init()：任何 import 本包的二进制（含 go test 测试进程）启动时
// 都会在真实用户配置目录读写密钥文件；惰性化后测试进程不再触碰真实环境。
func getEncryptor() *security.Encryptor {
	encryptorOnce.Do(func() {
		key, err := security.GetOrCreateEncryptionKey()
		if err != nil {
			logger.Errorf("failed to get encryption key: %v, generating a new one", err)
			key, err = security.GenerateAndSaveEncryptionKey()
			if err != nil {
				logger.Errorf("failed to generate new encryption key: %v, config encryption will be disabled", err)
			}
		}
		if key != "" {
			var encErr error
			encryptor, encErr = security.NewEncryptor(key)
			if encErr != nil {
				logger.Errorf("failed to create encryptor: %v, config encryption will be disabled", encErr)
			}
		}
	})
	return encryptor
}

func DefaultConfig() *Config {
	return &Config{
		Meta: Meta{
			App:     AppName,
			Author:  AppAuthor,
			Version: AppVersion,
		},
		NodeName:  "my-node",
		Community: generateRandomCommunity(),
		Key:       "",
		Supernode: "", // 空 = 跟随 DefaultSupernode；不落盘以便默认节点迁移时自动穿透
		IP:        "10.10.10.10",
		Mode:      "classic",

		// 极速模式默认值：空 = 跟随 DefaultRoomAPIURL，不落盘
		RoomAPIURL: "",
	}
}

// generateRandomCommunity 生成随机社区名
func generateRandomCommunity() string {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "sogame"
	}
	return "community-" + hex.EncodeToString(bytes)
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SoGame")
	_ = os.MkdirAll(path, 0700)
	return filepath.Join(path, "config.yaml"), nil
}

func LoadOrCreate() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		// 主文件不存在但 .bak 可能存在：Save 的 Remove+Rename 两步窗口
		// 崩溃/断电会留下"config.yaml 不存在、.bak 完好"的状态，此分支
		// 不查 .bak 直接 DefaultConfig 会把用户的房间身份覆盖掉。
		if backupCfg, backupErr := RestoreFromBackup(); backupErr == nil {
			logger.Infof("config file missing, restored from backup")
			migrateAndPersist(backupCfg)
			return backupCfg, nil
		}
		cfg := DefaultConfig()
		if err := Save(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		logger.Errorf("failed to parse config file: %v, trying to restore from backup", err)
		// 尝试从备份恢复
		backupCfg, backupErr := RestoreFromBackup()
		if backupErr == nil {
			logger.Infof("successfully restored config from backup")
			migrateAndPersist(backupCfg)
			return backupCfg, nil
		}
		// 备份也失败，创建默认配置
		logger.Errorf("failed to restore from backup: %v, creating default config", backupErr)
		defaultCfg := DefaultConfig()
		if saveErr := Save(defaultCfg); saveErr != nil {
			return nil, fmt.Errorf("failed to create default config: %w", saveErr)
		}
		return defaultCfg, fmt.Errorf("config file corrupted, restored to default config: %w", err)
	}

	if cfg.Key != "" {
		if enc := getEncryptor(); enc != nil {
			decryptedKey, err := enc.Decrypt(cfg.Key)
			if err != nil {
				// 解密失败（密钥文件丢失/重装系统后自动生成新密钥）时
				// 必须置空 Key：密文（"v2:"+base64，17+ 字符明文的密文
				// 必然超过 ValidateKey 的 64 字符上限）若继续参与校验，
				// 会触发 Validate 失败 → .bak 恢复（.bak 里同样是密文，
				// 同样失败）→ DefaultConfig 静默整体重置，用户的房间
				// 身份（community/密钥）无声消失。置空后 Validate 通过，
				// 用户只需在 UI 重新输入密钥。
				logger.Warnf("failed to decrypt key, cleared stored key (re-enter it in UI): %v", err)
				cfg.Key = ""
			} else {
				cfg.Key = decryptedKey
			}
		}
	}

	// 向后兼容：补充缺失的 Meta 字段
	if cfg.Meta.App == "" {
		cfg.Meta = Meta{
			App:     AppName,
			Author:  AppAuthor,
			Version: AppVersion,
		}
	}

	// 验证加载的配置
	if err := cfg.Validate(); err != nil {
		logger.Errorf("invalid config: %v, trying to restore from backup", err)
		// 尝试从备份恢复
		backupCfg, backupErr := RestoreFromBackup()
		if backupErr == nil {
			logger.Infof("successfully restored config from backup")
			migrateAndPersist(backupCfg)
			return backupCfg, nil
		}
		// 备份也失败，创建默认配置
		logger.Errorf("failed to restore from backup: %v, creating default config", backupErr)
		defaultCfg := DefaultConfig()
		if saveErr := Save(defaultCfg); saveErr != nil {
			return nil, fmt.Errorf("failed to create default config: %w", saveErr)
		}
		return defaultCfg, fmt.Errorf("config invalid, restored to default config: %w", err)
	}

	migrateAndPersist(&cfg)
	return &cfg, nil
}

func Save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	// 确保配置目录存在且权限正确
	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// 如果配置文件已存在，创建备份
	if _, err := os.Stat(path); err == nil {
		backupPath := path + ".bak"
		if _, err := os.Stat(backupPath); err == nil {
			// 删除旧备份
			if err := os.Remove(backupPath); err != nil {
				logger.Warnf("failed to remove old backup: %v", err)
			}
		}
		// 创建新备份
		data, err := os.ReadFile(path)
		if err != nil {
			logger.Warnf("failed to read config for backup: %v", err)
		} else {
			if err := os.WriteFile(backupPath, data, 0600); err != nil {
				logger.Warnf("failed to write backup file: %v", err)
			}
		}
	}

	// 更新 Meta 信息
	configCopy := *cfg
	configCopy.Meta = Meta{
		App:     AppName,
		Author:  AppAuthor,
		Version: AppVersion,
	}

	encryptedKey := cfg.Key
	if cfg.Key != "" {
		enc := getEncryptor()
		// 安全红线：加密器不可用时拒绝保存密钥，绝不明文落盘。
		if enc == nil {
			return fmt.Errorf("encryptor unavailable; refusing to save the key in plaintext")
		}
		encrypted, err := enc.Encrypt(cfg.Key)
		if err != nil {
			return fmt.Errorf("failed to encrypt key: %w", err)
		}
		encryptedKey = encrypted
	}

	configCopy.Key = encryptedKey

	data, err := yaml.Marshal(&configCopy)
	if err != nil {
		return err
	}

	// 原子写入（临时文件 + 替换）：直接截断写时崩溃/断电会留下半截
	// config.yaml；已有的 .bak 机制负责恢复，这里负责不让损坏发生。
	// 注：0600 在 Windows 上仅映射只读属性位，实际访问控制由
	// %AppData% 父目录 ACL（属主+SYSTEM+Administrators）承担。
	tmp, err := os.CreateTemp(configDir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp config file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close config file: %w", err)
	}
	// Windows 的 os.Rename 不能覆盖已存在目标，先删后换（.bak 已先行兜底）。
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to replace config file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate 验证配置的有效性
func (c *Config) Validate() error {
	if err := ValidateNodeName(c.NodeName); err != nil {
		return fmt.Errorf("invalid node name: %w", err)
	}
	if err := ValidateCommunity(c.Community); err != nil {
		return fmt.Errorf("invalid community: %w", err)
	}
	if err := ValidateKey(c.Key); err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}
	// Supernode 允许为空（空 = 跟随内置默认 DefaultSupernode）
	if c.Supernode != "" {
		if err := ValidateSupernode(c.Supernode); err != nil {
			return fmt.Errorf("invalid supernode: %w", err)
		}
	}
	if err := ValidateIP(c.IP); err != nil {
		return fmt.Errorf("invalid ip: %w", err)
	}
	return nil
}

// ValidateNodeName 验证节点名称
// 规则：长度 1-32，只能包含字母、数字、- 和 _
func ValidateNodeName(name string) error {
	if len(name) == 0 || len(name) > 32 {
		return fmt.Errorf("length must be between 1-32 characters")
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return fmt.Errorf("only alphanumeric characters, dash and underscore are allowed")
		}
	}
	return nil
}

// ValidateCommunity 验证 Community
// 规则：非空，长度 1-64，不能包含控制字符，不得以 '-' 开头
// （community 直接进入 edge.exe argv，以 '-' 开头会被参数解析吞并语义）。
func ValidateCommunity(community string) error {
	if len(community) == 0 || len(community) > 64 {
		return fmt.Errorf("length must be between 1-64 characters")
	}
	if community[0] == '-' {
		return fmt.Errorf("must not start with '-'")
	}
	// 检查是否包含控制字符
	for _, ch := range community {
		if ch < 32 || ch == 127 {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	return nil
}

// ValidateKey 验证密钥
// 规则：长度 8-64，不能包含控制字符
func ValidateKey(key string) error {
	// 允许空密钥（首次使用时）
	if key == "" {
		return nil
	}
	if len(key) < 8 {
		return fmt.Errorf("minimum length is 8 characters")
	}
	if len(key) > 64 {
		return fmt.Errorf("maximum length is 64 characters")
	}
	// 检查是否包含控制字符
	for _, ch := range key {
		if ch < 32 || ch == 127 {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	return nil
}

// ValidateIP 验证 IP 地址
func ValidateIP(ipStr string) error {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return fmt.Errorf("invalid IP address format")
	}
	return nil
}

// ValidateSupernode 验证 Supernode 地址 (IP:PORT 或 HOSTNAME:PORT)
func ValidateSupernode(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("must be in HOST:PORT format: %w", err)
	}

	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// 允许 IP 地址和主机名
	if ip := net.ParseIP(host); ip == nil {
		// 不是 IP 地址，验证为主机名
		if len(host) > 253 {
			return fmt.Errorf("hostname too long (max 253 characters)")
		}
		for _, ch := range host {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '.') {
				return fmt.Errorf("hostname contains invalid characters")
			}
		}
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("invalid port number (must be 1-65535)")
	}

	return nil
}

// RestoreFromBackup 从备份恢复配置
// 如果备份不存在返回错误
func RestoreFromBackup() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	backupPath := path + ".bak"

	// 检查备份文件是否存在
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("no backup file found at %s", backupPath)
	}

	// 读取备份文件
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse backup file: %w", err)
	}

	if cfg.Key != "" {
		if enc := getEncryptor(); enc != nil {
			decryptedKey, err := enc.Decrypt(cfg.Key)
			if err != nil {
				logger.Warnf("failed to decrypt key from backup, using raw key: %v", err)
			} else {
				cfg.Key = decryptedKey
			}
		}
	}

	// 备份同样必须过校验：损坏/被篡改的 .bak 若绕过 Validate 直接进入
	// 运行时并落盘，非法字段（畸形 IP/超长 community）会一路进入 edge argv。
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("backup config is invalid: %w", err)
	}

	return &cfg, nil
}

// ============================================================================
// 废弃入口迁移
// ============================================================================
//
// 背景：旧版本会把当时的默认 Room API 地址 / 默认中心节点固化进 config.yaml。
// 入口下线后，这些残留的显式值优先于新的内置默认值，导致全量存量客户端
// 请求落到无效路由。加载配置时按名单迁移为当前默认值即可无感修复。

// NormalizeRoomAPIURL 归一化 Room API 地址：去除空白与尾随斜杠；
// 命中废弃名单时返回当前默认值。空值原样返回（空 = 跟随内置默认）。
func NormalizeRoomAPIURL(rawURL string) string {
	value := strings.TrimSpace(rawURL)
	if value == "" {
		return ""
	}
	key := strings.ToLower(strings.TrimRight(value, "/"))
	if deprecatedRoomAPIURLs[key] {
		return DefaultRoomAPIURL
	}
	return value
}

// NormalizeSupernode 归一化中心节点地址：去除空白并统一小写；
// 命中废弃名单时返回当前默认节点。空值原样返回（空 = 跟随内置默认）。
// 邀请码中的节点地址也经此函数处理，使旧邀请码在节点下线后仍可用。
func NormalizeSupernode(address string) string {
	value := strings.TrimSpace(address)
	if value == "" {
		return ""
	}
	key := strings.ToLower(value)
	if deprecatedSupernodes[key] {
		return DefaultSupernode
	}
	return value
}

// MigrateDeprecatedEndpoints 将配置中已废弃的 Room API 地址 / 中心节点
// 迁移为"跟随内置默认"（置空），返回是否有字段被修改。
// 置空而非写入默认值常量：空值经 omitempty 不落盘，未来入口再切换时
// 被迁移用户可继续自动穿透，无需再次依赖废弃名单兜底。
func MigrateDeprecatedEndpoints(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	if key := strings.ToLower(strings.TrimRight(strings.TrimSpace(cfg.RoomAPIURL), "/")); deprecatedRoomAPIURLs[key] {
		logger.Infof("migrating deprecated room_api_url to follow built-in default")
		cfg.RoomAPIURL = ""
		changed = true
	}
	if key := strings.ToLower(strings.TrimSpace(cfg.Supernode)); deprecatedSupernodes[key] {
		logger.Infof("migrating deprecated supernode to follow built-in default")
		cfg.Supernode = ""
		changed = true
	}
	return changed
}

// migrateAndPersist 对已加载的配置执行废弃入口迁移；有变更时立即落盘
// （Save 自带 .bak 备份，迁移可回滚）。落盘失败仅记日志，不阻断启动。
func migrateAndPersist(cfg *Config) {
	if MigrateDeprecatedEndpoints(cfg) {
		if err := Save(cfg); err != nil {
			logger.Warnf("failed to persist migrated config: %v", err)
		}
	}
}
