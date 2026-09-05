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

package securestore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// 房主令牌与房间码同级保护：DPAPI 加密落盘 + 0600 权限 + 原子替换。
// 但使用独立的 magic/路径，与房间码互不兼容，防止文件互换误读。
const (
	ownerTokenMaxSize         = 96
	ownerTokenCiphertextLimit = 4 << 10
	ownerTokenEnvelopeVersion = byte(1)
)

var (
	ownerTokenEnvelopeMagic = []byte{'S', 'G', 'N', 'B', 'O', 'T'}
	ErrNoOwnerToken         = errors.New("no protected owner token")
)

// OwnerTokenStore 保存解散房间所需的房主令牌（仅房主端有）。
type OwnerTokenStore struct {
	path    string
	replace func(string, string) error
	mu      sync.Mutex
}

func NewOwnerTokenStore(path string) (*OwnerTokenStore, error) {
	if path == "" || filepath.Base(path) == "." {
		return nil, errors.New("protected owner token path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve protected owner token path")
	}
	return &OwnerTokenStore{path: absolute, replace: replaceFile}, nil
}

// DefaultOwnerTokenPath 与房间码放在同一目录（metadata 所在目录）。
func DefaultOwnerTokenPath() (string, error) {
	metadataPath, err := DefaultMetadataPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(metadataPath), "owner.token"), nil
}

func (s *OwnerTokenStore) Save(token []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateOwnerToken(token); err != nil {
		return err
	}
	ciphertext, err := protectForCurrentUser(token)
	if err != nil {
		return fmt.Errorf("protect owner token for current Windows user: %w", err)
	}
	defer clearBytes(ciphertext)
	if len(ciphertext) == 0 || len(ciphertext) > ownerTokenCiphertextLimit {
		return errors.New("protected owner token has an invalid size")
	}
	envelope := make([]byte, 0, len(ownerTokenEnvelopeMagic)+1+len(ciphertext))
	envelope = append(envelope, ownerTokenEnvelopeMagic...)
	envelope = append(envelope, ownerTokenEnvelopeVersion)
	envelope = append(envelope, ciphertext...)
	defer clearBytes(envelope)

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create protected owner token directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".owner-token-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary protected owner token: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict protected owner token permissions: %w", err)
	}
	if _, err := temporary.Write(envelope); err != nil {
		return fmt.Errorf("write protected owner token: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush protected owner token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close protected owner token: %w", err)
	}
	if err := s.replace(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace protected owner token: %w", err)
	}
	keepTemporary = false
	return nil
}

// Load 读取房主令牌；文件不存在返回 ErrNoOwnerToken(调用方按"非房主"处理)。
func (s *OwnerTokenStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoOwnerToken
	}
	if err != nil {
		return nil, fmt.Errorf("read protected owner token: %w", err)
	}
	defer file.Close()
	headerSize := len(ownerTokenEnvelopeMagic) + 1
	envelope, err := io.ReadAll(io.LimitReader(file, int64(ownerTokenCiphertextLimit+headerSize+1)))
	if err != nil {
		return nil, fmt.Errorf("read protected owner token: %w", err)
	}
	defer clearBytes(envelope)
	if len(envelope) <= headerSize || len(envelope) > ownerTokenCiphertextLimit+headerSize {
		return nil, errors.New("protected owner token envelope has an invalid size")
	}
	if !bytes.Equal(envelope[:len(ownerTokenEnvelopeMagic)], ownerTokenEnvelopeMagic) || envelope[len(ownerTokenEnvelopeMagic)] != ownerTokenEnvelopeVersion {
		return nil, errors.New("protected owner token envelope is unsupported")
	}
	cleartext, err := unprotectForCurrentUser(envelope[headerSize:])
	if err != nil {
		return nil, fmt.Errorf("unprotect owner token for current Windows user: %w", err)
	}
	if err := validateOwnerToken(cleartext); err != nil {
		clearBytes(cleartext)
		return nil, errors.New("protected owner token content is invalid")
	}
	return cleartext, nil
}

func (s *OwnerTokenStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear protected owner token: %w", err)
	}
	return nil
}

func validateOwnerToken(token []byte) error {
	if len(token) < 16 || len(token) > ownerTokenMaxSize {
		return errors.New("owner token has an invalid size")
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return errors.New("owner token contains invalid characters")
		}
	}
	return nil
}
