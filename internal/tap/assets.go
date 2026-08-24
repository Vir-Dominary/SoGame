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
	"fmt"
	"os"
	"path/filepath"
)

func FindDriverDir(baseDir, wd string) (string, error) {
	candidates := []string{
		filepath.Join(baseDir, "tap"),
		filepath.Join(baseDir, "installer", "tap"),
		filepath.Join(baseDir, "..", "installer", "tap"),
	}
	if wd != "" && wd != baseDir {
		candidates = append(candidates,
			filepath.Join(wd, "tap"),
			filepath.Join(wd, "installer", "tap"),
		)
	}

	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(filepath.Join(abs, "OemWin2k.inf")); err == nil {
			return abs, nil
		}
	}
	return "", fmt.Errorf("未找到 TAP 驱动文件目录 (OemWin2k.inf)")
}

func FindTapinstall(tapDir string) (string, error) {
	candidates := []string{
		filepath.Join(tapDir, "tapinstall.exe"),
		filepath.Join(tapDir, "devcon.exe"),
		`C:\Program Files\TAP-Windows\bin\tapinstall.exe`,
		`C:\Program Files\OpenVPN\bin\tapinstall.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 tapinstall.exe")
}
