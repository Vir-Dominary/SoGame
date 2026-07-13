package keys

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// deriveWithWG 使用 wg 命令推导公钥
func deriveWithWG(privateKey string) (string, error) {
	cmd := exec.Command("wg", "pubkey")
	cmd.Stdin = strings.NewReader(privateKey)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("wg pubkey: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// deriveWithGo 使用 Go 的 curve25519 实现推导公钥
func deriveWithGo(privateKey string) (string, error) {
	var privBytes [32]byte
	if n, err := base64Decode(privateKey, privBytes[:]); err != nil || n != 32 {
		return "", fmt.Errorf("invalid private key")
	}

	// Clamp private key
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64

	pubBytes, err := curve25519.X25519(privBytes[:], curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("x25519: %w", err)
	}

	return base64Encode(pubBytes), nil
}
