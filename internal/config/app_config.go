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

package config

const (
	AppName       = "SoGame"
	AppVersion    = "2.0"
	AppAuthor     = "vir_dominary"
	AppURL        = "https://github.com/vir-dominary"
	AppBilibili   = "https://space.bilibili.com/454851989"
	AppDesc       = "SoGame - 远程组网工具"
	AppSponsorURL = "https://www.ifdian.net/a/vir_dominary?utm_source=copylink&utm_medium=link"

	// DefaultRoomAPIURL 是极速模式（netbird）的默认 Room API 服务地址。
	// 指向 sogame-netbird 实际运行的控制平面（123.56.254.224）；
	// 本地开发可在 UI 设置或配置文件中临时指向本地 Mock（tools/room-api-mock）。
	// 注意：此处必须是所有客户端都能访问到的同一服务端，
	// 否则不同机器创建/加入的房间互不可见。
	DefaultRoomAPIURL = "http://123.56.254.224"
)
