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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	CurrentMetadataVersion = 1
	metadataFileLimit      = 16 << 10
)

var ErrNoRoomMetadata = errors.New("no saved room metadata")

type MetadataVersionError struct {
	Expected int
	Detected int
}

func (e *MetadataVersionError) Error() string {
	return fmt.Sprintf("room metadata version mismatch: expected %d, detected %d", e.Expected, e.Detected)
}

type RoomMetadata struct {
	Version       int       `json:"version"`
	RoomID        string    `json:"roomId"`
	ManagementURL string    `json:"managementUrl"`
	ProfileID     string    `json:"profileId"`
	CreatedAt     time.Time `json:"createdAt"`
	// RelayEnabled 记录该房间所属服务器是否允许 Relay 中继。
	// 由服务器随房间 enrollment 下发；客户端仅持久化并遵循。
	// 旧版本房间数据缺少该字段时默认 false（纯 P2P 优先）。
	RelayEnabled bool `json:"relayEnabled"`
}

type MetadataStore struct {
	path    string
	replace func(string, string) error
	mu      sync.Mutex
}

func NewMetadataStore(path string) (*MetadataStore, error) {
	if path == "" || filepath.Base(path) == "." {
		return nil, errors.New("room metadata path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve room metadata path")
	}
	return &MetadataStore{path: absolute, replace: replaceFile}, nil
}

func DefaultMetadataPath() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		var err error
		root, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user application data directory: %w", err)
		}
	}
	return filepath.Join(root, "Sogame", "NetBird", "room.json"), nil
}

func (s *MetadataStore) Path() string { return s.path }

func (s *MetadataStore) Load() (RoomMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return loadMetadata(s.path)
}

func (s *MetadataStore) Save(metadata RoomMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if metadata.Version == 0 {
		metadata.Version = CurrentMetadataVersion
	}
	if err := validateMetadata(metadata); err != nil {
		return err
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("encode room metadata")
	}
	if len(payload) > metadataFileLimit {
		return errors.New("room metadata exceeds size limit")
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create room metadata directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".room-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary room metadata: %w", err)
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
		return fmt.Errorf("restrict room metadata permissions: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write room metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush room metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close room metadata: %w", err)
	}
	if err := s.replace(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace room metadata: %w", err)
	}
	keepTemporary = false
	return nil
}

func (s *MetadataStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear room metadata: %w", err)
	}
	return nil
}

func loadMetadata(path string) (RoomMetadata, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return RoomMetadata{}, ErrNoRoomMetadata
	}
	if err != nil {
		return RoomMetadata{}, fmt.Errorf("open room metadata: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, metadataFileLimit+1))
	if err != nil {
		return RoomMetadata{}, fmt.Errorf("read room metadata: %w", err)
	}
	if len(payload) > metadataFileLimit {
		return RoomMetadata{}, errors.New("room metadata exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var metadata RoomMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return RoomMetadata{}, errors.New("decode room metadata")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return RoomMetadata{}, errors.New("room metadata contains trailing data")
	}
	if err := validateMetadata(metadata); err != nil {
		return RoomMetadata{}, err
	}
	return metadata, nil
}

func validateMetadata(metadata RoomMetadata) error {
	if metadata.Version != CurrentMetadataVersion {
		return &MetadataVersionError{Expected: CurrentMetadataVersion, Detected: metadata.Version}
	}
	if metadata.RoomID == "" || metadata.ProfileID == "" || metadata.CreatedAt.IsZero() {
		return errors.New("room metadata is incomplete")
	}
	managementURL, err := url.Parse(metadata.ManagementURL)
	if err != nil || (managementURL.Scheme != "https" && managementURL.Scheme != "http") || managementURL.Host == "" || managementURL.User != nil {
		return errors.New("room metadata management URL is invalid")
	}
	return nil
}
