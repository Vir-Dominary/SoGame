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

// update-sign 为热更新包计算 SHA256 并用离线私钥生成 manifest 签名。
// 签名覆盖内容与客户端 updater.signaturePayload 严格一致：
//
//	version + "\n" + lowercase(sha256Hex)
//
// 用法：
//
//	update-sign -key sogame-update.key -version 2.2 -zip publish/sogame-update-2.2.zip
//
// stdout 输出两行：sha256=<hex> 与 signature=<base64>，
// 供 scripts/publish-update.ps1 写入 update.json。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	keyPath := flag.String("key", "", "私钥文件（update-keygen 生成，base64 单行）")
	version := flag.String("version", "", "版本号（与 update.json 的 version 一致）")
	zipPath := flag.String("zip", "", "更新包 zip 路径")
	flag.Parse()
	if *keyPath == "" || *version == "" || *zipPath == "" {
		fmt.Fprintln(os.Stderr, "usage: update-sign -key <keyfile> -version <version> -zip <zipfile>")
		os.Exit(2)
	}

	keyBytes, err := os.ReadFile(*keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read key:", err)
		os.Exit(1)
	}
	privateKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyBytes)))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		fmt.Fprintln(os.Stderr, "invalid private key file")
		os.Exit(1)
	}

	file, err := os.Open(*zipPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open zip:", err)
		os.Exit(1)
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		file.Close()
		fmt.Fprintln(os.Stderr, "hash zip:", err)
		os.Exit(1)
	}
	file.Close()
	sha256Hex := hex.EncodeToString(hasher.Sum(nil))

	// 与 internal/updater/updater.go 的 signaturePayload 保持一致。
	payload := []byte(*version + "\n" + strings.ToLower(strings.TrimSpace(sha256Hex)))
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), payload)

	fmt.Printf("sha256=%s\n", sha256Hex)
	fmt.Printf("signature=%s\n", base64.StdEncoding.EncodeToString(signature))
}
