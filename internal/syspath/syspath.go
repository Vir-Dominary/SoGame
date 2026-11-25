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

package syspath

import (
	"os"
	"path/filepath"
)

// System32 返回系统自带可执行程序在 System32 下的绝对路径。
//
// 安全红线：主程序以管理员身份运行，经 PATH（或 Inno Exec 的当前目录
// 搜索）解析系统程序是提权代码执行面——用户级 PATH 目录放置同名
// ping.exe/netsh.exe 即可借 SoGame 的管理员上下文执行任意代码。
// 全部系统程序（ping/netsh/pnputil/tasklist/taskkill/ipconfig/notepad 等）
// 必须经此函数取路径，不做 PATH 回退。
func System32(name string) string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", name)
	}
	return filepath.Join(`C:\Windows`, "System32", name)
}
