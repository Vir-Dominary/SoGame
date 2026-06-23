package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Encryptor 加密器
type Encryptor struct {
	key []byte
}

// NewEncryptor 创建新的加密器
// key 必须是 16, 24 或 32 字节长度
func NewEncryptor(key string) (*Encryptor, error) {
	keyBytes := []byte(key)
	// 确保 key 长度符合 AES 要求
	if len(keyBytes) < 32 {
		// 填充到 32 字节
		paddedKey := make([]byte, 32)
		copy(paddedKey, keyBytes)
		keyBytes = paddedKey
	} else if len(keyBytes) > 32 {
		// 截断到 32 字节
		keyBytes = keyBytes[:32]
	}

	return &Encryptor{key: keyBytes}, nil
}

// Encrypt 加密字符串
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}

	plaintextBytes := []byte(plaintext)
	ciphertext := make([]byte, aes.BlockSize+len(plaintextBytes))
	iv := ciphertext[:aes.BlockSize]

	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintextBytes)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt 解密字符串
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	ciphertextBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(e.key)
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
