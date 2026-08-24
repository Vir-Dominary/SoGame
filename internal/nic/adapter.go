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

package nic

import "fmt"

// Info 表示一块网卡的关键信息；InterfaceLuid 为系统内唯一标识。
type Info struct {
	FriendlyName string
	Description  string
	Luid         uint64
	AdminStatus  uint32
	OperStatus   uint32
}

// AdminText 返回管理状态（启用/禁用）的中文描述。
func (i Info) AdminText() string {
	switch i.AdminStatus {
	case 1:
		return "启用"
	case 2:
		return "禁用"
	case 3:
		return "测试"
	default:
		return fmt.Sprintf("其它(%d)", i.AdminStatus)
	}
}

// OperText 返回运行状态的中文描述。
func (i Info) OperText() string {
	switch i.OperStatus {
	case 1:
		return "已连接"
	case 2:
		return "已断开"
	case 4:
		return "未知"
	case 5:
		return "休眠"
	case 6:
		return "不存在"
	case 7:
		return "下层断开"
	default:
		return fmt.Sprintf("其它(%d)", i.OperStatus)
	}
}

// ErrNotFound 表示未找到指定网卡。
var ErrNotFound = fmt.Errorf("未找到匹配的网卡")

// 管理状态常量（MIB_IF_ADMIN_STATUS_*，只读查询与 WaitAdminStatus 使用）。
const (
	AdminUp   uint32 = 1
	AdminDown uint32 = 2
)
