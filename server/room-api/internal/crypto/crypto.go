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

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// Hash 是旧版无盐 SHA256 查询键，仅保留用于既有数据的在线迁移
//（见 rooms.lookupRoomByCode 的回退路径）；新写入一律用 HashCode。
func Hash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

// HashCode 用 HMAC-SHA256(pepper, code) 作为房间码查询键。
// 房间码只有约 60bit 熵且本身就是完整入会凭证：无盐 SHA256 在 DB 泄露
// 后可被离线枚举反查明文码（再经在线 Join 进入任意活跃房间）；HMAC 把
// 离线枚举成本拉到不可行，同时保留等值查询能力。
func HashCode(pepper []byte, code string) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func Seal(key []byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Open(key []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext is too short")
	}
	nonce, data := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, data, nil)
}