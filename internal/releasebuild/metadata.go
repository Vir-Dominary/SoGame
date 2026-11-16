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

package releasebuild

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed netbird-release.json
var releaseFS embed.FS

type Metadata struct {
	SchemaVersion int             `json:"schemaVersion"`
	Version       string          `json:"version"`
	ServerImage   string          `json:"serverImage"`
	PackagingMode string          `json:"packagingMode"`
	HelperSHA256  string          `json:"helperSha256,omitempty"`
	WindowsX64    WindowsArtifact `json:"windowsX64"`
}

type WindowsArtifact struct {
	Artifact  string            `json:"artifact"`
	URL       string            `json:"url"`
	Size      int64             `json:"size"`
	SHA256    string            `json:"sha256"`
	Publisher Publisher         `json:"publisher"`
	Install   InstallProperties `json:"install"`
}

type Publisher struct {
	SubjectCommonName              string `json:"subjectCommonName"`
	Organization                   string `json:"organization"`
	CertificateThumbprintAtRelease string `json:"certificateThumbprintAtRelease"`
}

type InstallProperties struct {
	ProductCode string   `json:"productCode"`
	Executable  string   `json:"executable"`
	Arguments   []string `json:"arguments"`
}

// helperSHA256Override 由构建脚本经 -ldflags "-X sogame/internal/releasebuild.helperSHA256Override=<hash>"
// 注入：构建顺序为"先编译 sogame-helper.exe → 计算其 SHA256 → 再编译
// 主程序并注入"，使主程序内嵌的 helper 完整性校验（prepareElevation）
// 真正生效。此前元数据 JSON 无 helperSha256 字段，校验被静默跳过——
// 安装到用户可写目录时，同用户进程可替换 helper 借 UAC 提权。
var helperSHA256Override string

func Load() (Metadata, error) {
	data, err := releaseFS.ReadFile("netbird-release.json")
	if err != nil {
		return Metadata{}, fmt.Errorf("read embedded NetBird release metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("parse embedded NetBird release metadata: %w", err)
	}
	if metadata.SchemaVersion != 1 || metadata.Version == "" || metadata.WindowsX64.SHA256 == "" {
		return Metadata{}, fmt.Errorf("invalid embedded NetBird release metadata")
	}
	if helperSHA256Override != "" {
		metadata.HelperSHA256 = helperSHA256Override
	}
	return metadata, nil
}
