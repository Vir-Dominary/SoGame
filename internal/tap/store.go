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

package tap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const knownAdapterFileName = "tap-adapter.json"

var (
	userConfigDir = os.UserConfigDir
	now           = time.Now
)

type KnownAdapter struct {
	NetCfgInstanceID string    `json:"netcfg_instance_id,omitempty"`
	LUID             uint64    `json:"luid,omitempty"`
	FriendlyName     string    `json:"friendly_name,omitempty"`
	Description      string    `json:"description,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

func KnownAdapterPath() (string, error) {
	root, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("get user config dir: %w", err)
	}
	return filepath.Join(root, "SoGame", knownAdapterFileName), nil
}

func LoadKnownAdapter() (*KnownAdapter, error) {
	path, err := KnownAdapterPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read known TAP adapter: %w", err)
	}

	var adapter KnownAdapter
	if err := json.Unmarshal(data, &adapter); err != nil {
		return nil, fmt.Errorf("parse known TAP adapter: %w", err)
	}
	adapter.NetCfgInstanceID = strings.TrimSpace(adapter.NetCfgInstanceID)
	adapter.FriendlyName = strings.TrimSpace(adapter.FriendlyName)
	adapter.Description = strings.TrimSpace(adapter.Description)
	return &adapter, nil
}

func SaveKnownAdapter(adapter KnownAdapter) error {
	path, err := KnownAdapterPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create known TAP adapter dir: %w", err)
	}

	adapter.NetCfgInstanceID = strings.TrimSpace(adapter.NetCfgInstanceID)
	adapter.FriendlyName = strings.TrimSpace(adapter.FriendlyName)
	adapter.Description = strings.TrimSpace(adapter.Description)
	adapter.UpdatedAt = now().UTC()

	data, err := json.MarshalIndent(adapter, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal known TAP adapter: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write known TAP adapter: %w", err)
	}
	return nil
}

func DeleteKnownAdapter() error {
	path, err := KnownAdapterPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete known TAP adapter: %w", err)
	}
	return nil
}
