// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors

package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	secret := "0123456789abcdef0123456789abcdef"
	sealed, err := enc.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, ciphertextV2Prefix) {
		t.Fatalf("v2 ciphertext must carry version prefix, got %q", sealed)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	plain, err := enc.Decrypt(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != secret {
		t.Fatalf("round trip mismatch: %q", plain)
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	enc, _ := NewEncryptor(testKey(t))
	sealed, _ := enc.Encrypt("secret-key-material")
	// 翻转密文的一个字节：GCM 必须报错（v1 CFB 会静默解出乱码）。
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, ciphertextV2Prefix))
	raw[len(raw)-1] ^= 0xFF
	tampered := ciphertextV2Prefix + base64.StdEncoding.EncodeToString(raw)
	if _, err := enc.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext must be rejected")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	encA, _ := NewEncryptor(testKey(t))
	encB, _ := NewEncryptor(testKey(t))
	sealed, _ := encA.Encrypt("secret")
	if _, err := encB.Decrypt(sealed); err == nil {
		t.Fatal("wrong key must fail authentication, not return garbage")
	}
}

// TestDecryptLegacyCFBCiphertext 旧版（AES-CFB + base64 文本截断派生 key）
// 写入的密文必须仍可解密，保证老用户升级后配置不丢。
func TestDecryptLegacyCFBCiphertext(t *testing.T) {
	key := testKey(t)
	// 用 v1 算法构造密文：CFB + 旧派生 key（base64 文本截断/填充 32B）
	legacyKey := make([]byte, 32)
	copy(legacyKey, []byte(key))
	block, _ := aes.NewCipher(legacyKey)
	secret := "legacy-secret"
	buf := make([]byte, aes.BlockSize+len(secret))
	if _, err := io.ReadFull(rand.Reader, buf[:aes.BlockSize]); err != nil {
		t.Fatal(err)
	}
	cipher.NewCFBEncrypter(block, buf[:aes.BlockSize]).XORKeyStream(buf[aes.BlockSize:], []byte(secret))
	legacyCiphertext := base64.StdEncoding.EncodeToString(buf)

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := enc.Decrypt(legacyCiphertext)
	if err != nil {
		t.Fatalf("legacy ciphertext must stay readable: %v", err)
	}
	if plain != secret {
		t.Fatalf("legacy round trip mismatch: %q", plain)
	}
}

func TestNewEncryptorRejectsEmptyKey(t *testing.T) {
	if _, err := NewEncryptor(""); err == nil {
		t.Fatal("empty key must be rejected")
	}
}

// TestNewEncryptorNonBase64KeyFallsBackToSHA256 非 base64 密钥按 SHA256 派生，
// 且两次派生一致（可用性）。
func TestNewEncryptorNonBase64KeyFallsBackToSHA256(t *testing.T) {
	enc1, err1 := NewEncryptor("not-base64!!")
	enc2, err2 := NewEncryptor("not-base64!!")
	if err1 != nil || err2 != nil {
		t.Fatal("non-base64 key must be accepted via SHA256 derivation")
	}
	sealed, _ := enc1.Encrypt("x")
	if _, err := enc2.Decrypt(sealed); err != nil {
		t.Fatal("same input must derive the same key")
	}
}
