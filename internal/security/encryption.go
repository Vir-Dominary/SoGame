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

package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Encryptor 经典模式 config.yaml 密钥字段的加密器。
//
// v2 设计（2026-11 修复）：
//   - AES-256-GCM（带认证）：替代旧版 AES-CFB——CFB 无 MAC，密文可被
//     比特翻转篡改而解密不报错。
//   - 密钥正确使用：密钥文件内容是 base64(32B)，v1 错误地把 base64 文本
//     的前 32 个 ASCII 字符当 AES key（熵损且密钥空间受限）；v2 解码后使用。
//   - 密文版本前缀 "v2:"：旧 CFB 密文用 v1 派生 key 解密（向后兼容），
//     重新保存时自动升级为 GCM，用户无感迁移。
//   - 残余限制：密钥文件与密文同目录（%AppData%\SoGame\），加密仅防
//     "顺手拷贝 config.yaml"的场景；长期方向是迁移 DPAPI（同 securestore）。
type Encryptor struct {
	key       []byte // v2：base64 解码后的 32B（或 SHA256 派生）
	legacyKey []byte // v1 派生（base64 文本截断/填充 32B），仅用于解密旧密文
}

const ciphertextV2Prefix = "v2:"

// NewEncryptor 创建加密器。key 为密钥文件内容（期望是 base64(32B)）。
// 非 base64 或长度不符时按 SHA256 派生（不再使用截断/填充）。
func NewEncryptor(key string) (*Encryptor, error) {
	if key == "" {
		return nil, errors.New("encryption key is empty")
	}
	enc := &Encryptor{}
	if decoded, err := base64.StdEncoding.DecodeString(key); err == nil && len(decoded) == 32 {
		enc.key = decoded
	} else {
		sum := sha256.Sum256([]byte(key))
		enc.key = sum[:]
	}
	// v1 派生规则（原样保留）：仅用于解密旧版本写入的 CFB 密文。
	legacy := make([]byte, 32)
	copy(legacy, []byte(key))
	enc.legacyKey = legacy
	return enc, nil
}

// Encrypt 加密字符串（AES-256-GCM，nonce 前置，"v2:" 版本前缀）。
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertextV2Prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密字符串：v2 密文走 GCM（认证失败即篡改，显式报错）；
// 无前缀的按 v1 CFB 密文用 v1 派生 key 解密（向后兼容）。
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if rest, ok := strings.CutPrefix(ciphertext, ciphertextV2Prefix); ok {
		return e.decryptV2(rest)
	}
	return e.decryptLegacyCFB(ciphertext)
}

func (e *Encryptor) decryptV2(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode v2 ciphertext: %w", err)
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("ciphertext authentication failed (tampered or wrong key)")
	}
	return string(plain), nil
}

// decryptLegacyCFB 解密 v1 密文（AES-CFB 无认证 + v1 派生 key）。
// 仅为读取旧版本配置保留；解密成功后由上层重新保存即自动升级为 v2。
func (e *Encryptor) decryptLegacyCFB(ciphertext string) (string, error) {
	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(e.legacyKey)
	if err != nil {
		return "", err
	}
	if len(ciphertextBytes) < aes.BlockSize {
		return "", errors.New("ciphertext too short")
	}
	iv := ciphertextBytes[:aes.BlockSize]
	ciphertextBytes = ciphertextBytes[aes.BlockSize:]
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertextBytes, ciphertextBytes)
	return string(ciphertextBytes), nil
}

// GetOrCreateEncryptionKey 获取或创建加密密钥
// 密钥存储在用户配置目录中
func GetOrCreateEncryptionKey() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	keyFile := filepath.Join(configDir, "SoGame", ".key")

	if keyBytes, err := os.ReadFile(keyFile); err == nil {
		return string(keyBytes), nil
	}

	return generateAndSaveKey(keyFile)
}

// GenerateAndSaveEncryptionKey 生成新的加密密钥并保存
// 当原有密钥文件丢失或损坏时调用
func GenerateAndSaveEncryptionKey() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	keyFile := filepath.Join(configDir, "SoGame", ".key")
	return generateAndSaveKey(keyFile)
}

func generateAndSaveKey(keyFile string) (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}

	encodedKey := base64.StdEncoding.EncodeToString(key)

	if err := os.MkdirAll(filepath.Dir(keyFile), 0700); err != nil {
		return "", err
	}

	if err := os.WriteFile(keyFile, []byte(encodedKey), 0600); err != nil {
		return "", err
	}

	return encodedKey, nil
}
