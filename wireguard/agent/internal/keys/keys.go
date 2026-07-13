package keys

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// KeyPair 保存 WireGuard 密钥对
type KeyPair struct {
	PrivateKey string
	PublicKey  string
}

// Generate 生成新的 WireGuard 密钥对
// WireGuard 密钥为 32 字节随机数据的 base64 编码
func Generate() (*KeyPair, error) {
	privBytes := make([]byte, 32)
	if _, err := rand.Read(privBytes); err != nil {
		return nil, fmt.Errorf("generate private key: %w", err)
	}

	// Clamp private key per WireGuard spec (RFC 7748 clamping)
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64

	privKey := base64.StdEncoding.EncodeToString(privBytes)
	pubKey, err := DerivePublicKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	return &KeyPair{
		PrivateKey: privKey,
		PublicKey:  pubKey,
	}, nil
}

// DerivePublicKey 从私钥推导公钥（调用 wg pubkey）
func DerivePublicKey(privateKey string) (string, error) {
	// 优先使用 wg 命令推导公钥
	if pub, err := deriveWithWG(privateKey); err == nil {
		return pub, nil
	}
	// 如果 wg 不可用，使用 Go 实现的 Curve25519
	return deriveWithGo(privateKey)
}

// Save 保存密钥对到文件
func Save(kp *KeyPair, dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}

	privPath := filepath.Join(dir, "private.key")
	if err := os.WriteFile(privPath, []byte(kp.PrivateKey), 0600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	pubPath := filepath.Join(dir, "public.key")
	if err := os.WriteFile(pubPath, []byte(kp.PublicKey), 0600); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}

	return nil
}

// Load 从文件加载密钥对
func Load(dir string) (*KeyPair, error) {
	privBytes, err := os.ReadFile(filepath.Join(dir, "private.key"))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	pubBytes, err := os.ReadFile(filepath.Join(dir, "public.key"))
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}

	return &KeyPair{
		PrivateKey: string(privBytes),
		PublicKey:  string(pubBytes),
	}, nil
}

// LoadOrCreate 加载或生成密钥对
func LoadOrCreate(dir string) (*KeyPair, error) {
	if kp, err := Load(dir); err == nil {
		return kp, nil
	}

	kp, err := Generate()
	if err != nil {
		return nil, err
	}
	if err := Save(kp, dir); err != nil {
		return nil, err
	}
	return kp, nil
}
