package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

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
	Supernode string `yaml:"supernode"`
	IP        string `yaml:"ip"`
}

// encryptor 全局加密器
var encryptor *security.Encryptor

func init() {
	key, err := security.GetOrCreateEncryptionKey()
	if err != nil {
		logger.Errorf("failed to get encryption key: %v, generating a new one", err)
		key, err = security.GenerateAndSaveEncryptionKey()
		if err != nil {
			logger.Errorf("failed to generate new encryption key: %v, config encryption will be disabled", err)
		}
	}
	if key != "" {
		encryptor, _ = security.NewEncryptor(key)
	}
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
		Supernode: "8.148.244.159:10090",
		IP:        "10.10.10.10",
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

	// 解密密钥
	if cfg.Key != "" {
		decryptedKey, err := encryptor.Decrypt(cfg.Key)
		if err != nil {
			logger.Warnf("failed to decrypt key, using raw key: %v", err)
		} else {
			cfg.Key = decryptedKey
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

	// 加密密钥
	encryptedKey, err := encryptor.Encrypt(cfg.Key)
	if err != nil {
		return fmt.Errorf("failed to encrypt key: %w", err)
	}

	configCopy.Key = encryptedKey

	data, err := yaml.Marshal(&configCopy)
	if err != nil {
		return err
	}

	// 写入配置文件，设置严格的权限（仅所有者可读写）
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// 在 Windows 上，尝试设置文件权限以增强安全性
	// 注意：Windows 的权限模型与 Unix 不同，这里只是做一个尝试
	if err := setFilePermissions(path); err != nil {
		logger.Warnf("failed to set file permissions: %v", err)
	}

	return nil
}

// setFilePermissions 在不同平台上设置文件权限
// Windows 上 os.WriteFile 的 0600 模式已足够，不再调用 icacls 以避免杀毒软件误报
func setFilePermissions(path string) error {
	if !isWindows() {
		return nil
	}
	// Windows 上文件权限由 NTFS ACL 控制，0600 模式在 Windows 上
	// 仅设置只读属性，实际安全性由文件所在目录的 ACL 保证。
	// 配置文件存储在 %AppData%\SoGame 下，该目录默认只有当前用户可访问。
	return nil
}

// isWindows 检查当前是否为 Windows 系统
func isWindows() bool {
	return runtime.GOOS == "windows"
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
	if err := ValidateSupernode(c.Supernode); err != nil {
		return fmt.Errorf("invalid supernode: %w", err)
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
// 规则：非空，长度 1-64，不能包含控制字符
func ValidateCommunity(community string) error {
	if len(community) == 0 || len(community) > 64 {
		return fmt.Errorf("length must be between 1-64 characters")
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

	// 解密密钥
	if cfg.Key != "" {
		decryptedKey, err := encryptor.Decrypt(cfg.Key)
		if err != nil {
			// 解密失败，可能是未加密的旧配置，尝试使用原始密钥
			logger.Warnf("failed to decrypt key from backup, using raw key: %v", err)
		} else {
			cfg.Key = decryptedKey
		}
	}

	return &cfg, nil
}
