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

var AppVersion = "2.1"

const (
	AppName       = "SoGame"
	AppAuthor     = "vir_dominary"
	AppURL        = "https://github.com/vir-dominary"
	AppBilibili   = "https://space.bilibili.com/454851989"
	AppDesc       = "SoGame - 远程组网工具"
	AppSponsorURL = "https://www.ifdian.net/a/vir_dominary?utm_source=copylink&utm_medium=link"
	UpdateURL = "https://virdy.cn/sogame/update.json"

	// UpdateManifestPublicKey 是热更新 manifest 的 ed25519 验签公钥（base64）。
	// 签名私钥由发布者离线保管（绝不可入库）；生成密钥对用
	// tools/update-keygen，发布签名集成在 scripts/publish-update.ps1。
	// 空字符串表示未启用验签（向后兼容：跳过签名校验）；
	// 一旦填入公钥，manifest 必须携带有效签名，否则更新被拒绝。
	UpdateManifestPublicKey = "PTqc8fX3tM1iQrAl/64tSB6Y/TbrW/iEZc9Z4e7iPqc="

	// DefaultRoomAPIURL 是极速模式（netbird）的默认 Room API 服务地址。
	// 指向 legengen.top（traefik 的 legengen-rooms 路由，HTTPS 加密传输；
	// virdy.cn 是主站/更新服务域名，没有 /rooms 路由——勿用）。
	// 本地开发可在 UI 设置或配置文件中临时指向本地 Mock（tools/room-api-mock）。
	// 注意：此处必须是所有客户端都能访问到的同一服务端，
	// 否则不同机器创建/加入的房间互不可见。
	DefaultRoomAPIURL = "https://legengen.top"

	// DefaultSupernode 是经典模式（n2n）的默认中心节点地址。
	// 配置文件中 supernode 为空时表示跟随此内置默认值，
	// 因此未来默认节点迁移时未显式选择过节点的客户端可自动跟随。
	DefaultSupernode = "8.148.244.159:10090"
)

// deprecatedRoomAPIURLs 是已下线的 Room API 入口名单（归一化后的小写地址）。
// 旧版本会把当时的默认值固化进 config.yaml，入口下线后这些残留值会导致
// 请求落到无效路由；LoadOrCreate 加载时会将名单中的值迁移为当前默认值
// （见 MigrateDeprecatedEndpoints）。下线新入口时只需在此追加地址并发版。
var deprecatedRoomAPIURLs = map[string]bool{
	"http://123.56.254.224": true, // 明文 IP 入口，2026-09-24 下线
	"http://virdy.cn":       true,
	"https://virdy.cn":      true, // virdy.cn 无 /rooms 路由（SPA fallback 返回 HTML）
}

// deprecatedSupernodes 是已下线的经典模式中心节点名单（host:port，小写）。
// 当前为空：尚无节点退役。节点下线时在此追加地址，存量配置与旧邀请码
// 中的该地址会被自动替换为 DefaultSupernode。
var deprecatedSupernodes = map[string]bool{}

// STUNServerCandidates 是 NAT 探测的候选服务器池（顺序即优先级）：
// 自建服务器为主，公共服务器兜底。探测按顺序故障转移：第一台成功的
// 作为主服务器，第二阶段再排除它取下一台可达者用于 NAT 类型对比。
// 自建 stun.virdy.cn 部署 coturn（仅 STUN，UDP 3478）后应保持首位；
// 未部署期间公共候选保证功能可用。
var STUNServerCandidates = []string{
	"stun.virdy.cn:3478",       // 自建（coturn，运维侧待部署）
	"stun.qq.com:3478",         // 腾讯公共 STUN
	"stun.miwifi.com:3478",     // 小米公共 STUN
	"turn.cloudflare.com:3478", // Cloudflare 公共 STUN（海外兜底）
}
