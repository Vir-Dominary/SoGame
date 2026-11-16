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

// update-keygen 生成热更新 manifest 签名用的 ed25519 密钥对。
//
// 用法：
//
//	go run ./tools/update-keygen -out sogame-update.key
//
// 输出：
//   - 私钥文件（base64 单行，0600）：由发布者离线保管，绝不可提交入库；
//   - stdout 打印公钥（base64）：填入 internal/config/app_config.go 的
//     UpdateManifestPublicKey 常量后发版，客户端即强制验签。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "sogame-update.key", "私钥输出路径（base64 单行，0600）")
	flag.Parse()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate key:", err)
		os.Exit(1)
	}
	if _, err := os.Stat(*out); err == nil {
		fmt.Fprintln(os.Stderr, "refusing to overwrite existing key file:", *out)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(privateKey)+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write private key:", err)
		os.Exit(1)
	}
	fmt.Println("私钥已写入（离线保管，绝不可入库）:", *out)
	fmt.Println("公钥（填入 internal/config/app_config.go 的 UpdateManifestPublicKey）:")
	fmt.Println(base64.StdEncoding.EncodeToString(publicKey))
}
