# AGENTS.md — SoGame 项目指引

> 本文件是供 AI 编码代理与开发者阅读的项目权威文档：架构、配置、约定、构建与测试方式。
> 人类向的介绍见 [README.md](README.md)；部署运维见 [deploy/DEPLOY.md](deploy/DEPLOY.md)。

## 1. 项目概述

SoGame 是 Windows 桌面的轻量级 P2P 虚拟局域网游戏联机工具（Wails v2 + Go + React）。
无需公网 IP，创建/加入房间即可互联。两种联机模式并存：

| | 经典模式（classic） | 极速模式（express） |
|---|---|---|
| 协议 | n2n（edge.exe + supernode） | WireGuard（NetBird daemon v0.74.7） |
| 网卡 | TAP-Windows V9 | Wintun（随 NetBird MSI 安装） |
| NAT 穿透 | supernode 中继 | ICE（STUN），可选 Relay 回退 |
| 房间模型 | 房间名 community + 密钥，邀请链接分享 | 房间码（`XXXX-XXXX-XXXX`，12 字符） |
| 控制面 | 无（仅 supernode） | Room API + NetBird Management/Signal |
| 子网 | 10.10.10.0/24 | NetBird 分配 100.x 虚拟 IP |

默认模式为 `classic`（配置文件空值向后兼容）。仅支持 Windows x64。

## 2. 仓库结构

两个 Go module：

- **主 module**（根目录 `go.mod`，模块名 `sogame`）：客户端 + 全部 `internal/` 包 + `cmd/` + `tools/`
- **room-api module**（`server/room-api/go.mod`）：独立部署的服务端，无外部第三方依赖（仅标准库 + SQLite 驱动）

```
main.go                  # Wails 入口；--update-apply 自更新子进程模式
cmd/
  sogame-helper/         # UAC 提权辅助程序（安装/修复/移除 NetBird MSI）
  nicctl/                # 网卡诊断工具
  taptest/               # TAP 适配器测试工具
internal/                # 客户端全部业务逻辑（见 §4 源码地图）
frontend/                # React 前端（Vite + Wails 绑定，wailsjs/ 为生成代码）
server/room-api/         # Room API 服务端（独立 module）
installer/               # Inno Setup 打包（sogame.iss + build-installer.ps1）
scripts/                 # build-all.ps1（一键构建）、publish-update.ps1（热更新发布）
deploy/                  # DEPLOY.md + nginx 配置片段
tools/                   # room-api-mock（联调替身，127.0.0.1:9099）、room-api-test.ps1
docs/                    # 经典模式 TAP 测试记录
wireguard/               # 极速模式说明（README.md，详细叙述见本文档）
```

## 3. 极速模式架构

```text
┌─────────────────────────────────────────────────────────────┐
│  Wails 桌面 UI（frontend/src/App.jsx）                        │
└────────────┬────────────────────────────────────────────────┘
             │ Wails 绑定（Express* 方法）
             ▼
┌─────────────────────────────────────────────────────────────┐
│  ExpressController（internal/webui/express.go）              │
│  房间生命周期、命令互斥（BusyCommand）、错误映射、状态广播      │
└────────────┬───────────────────────────────┬────────────────┘
             │ HTTPS                          │ gRPC 127.0.0.1:41731
             ▼                                ▼
┌────────────────────────┐         ┌──────────────────────────┐
│ Room API 服务器         │         │ NetBird daemon（系统服务） │
│ server/room-api         │         │ 官方 v0.74.7 MSI 安装      │
│ 房间 ↔ Group+SetupKey   │────→    │ WireGuard/ICE/可选 Relay   │
│ +Policy 薄映射（SQLite）│ PAT     │ Management/Signal 注册同步  │
└────────────────────────┘         └──────────────────────────┘
```

关键设计决策：

1. **Room API 是薄映射层**：房间 = NetBird Group + Setup Key + 同组互通 Policy；
   不实现 NetBird 的登录/OIDC，Setup Key 匿名注册，用户只需昵称 + 房间码。
2. **官方 MSI 不修改**：daemon 由 `sogame-helper.exe` UAC 提权安装为系统服务；
   安装前校验 MSI 的 SHA256 + Authenticode 签名 + publisher CN/O（`internal/nbdaemon/artifact_windows.go`）。
3. **凭证分级保护**：房间码与 owner token 经 Windows DPAPI（current-user）加密落盘
   （`internal/securestore/`）；服务端 SQLite 中 setup key 以 AES-GCM 加密、
   房间码与 owner token 仅存 SHA256 哈希。
4. **Relay 由服务器掌握**：开关 `ROOM_API_RELAY_ENABLED` 随房间下发，客户端被动遵循（见 §6.3）。

### 房间状态机（internal/session/state.go）

```text
NoRoom → Enrolling → ControlPlaneConnected → WaitingForPeer → ConnectingPeer
                                                              ├→ ConnectedP2P
                                                              └→ ConnectedRelay（仅 relay_enabled=true）
任意态 → Reconnecting（控制面断开/对端超时/重连中）→ 恢复
任意态 → RecoverableError（可恢复错误，UI 提供"修复"）
```

派生规则要点：P2P 直连优先；`RelayAllowed=false` 时中继路径不视为已连接
（`preferredConnectedPath`）；对端连接超时 30s（`peerWaitTimeout`）转 Reconnecting。

### 房主机制

- Create 响应额外返回 `owner_token`（仅此一次下发，DPAPI 落盘）。
- 房主每 60s 发送心跳（`ownerHeartbeatInterval`，`internal/session/service.go`）；
  服务端对 30 分钟无心跳的房间执行回收（删除 Group/SetupKey/Policy 并禁用房间）。
- 房主可解散房间（Close，owner token 鉴权）；成员收到 `room_closed` 后自动清理本地状态。
- 房主离线由服务端看门狗兜底；客户端另有身份自愈：进房必重置 daemon 身份，
  归属漂移/控制面死亡时自动以同一房间码重新 Join（`internal/session/repair.go`，
  4 次观测防抖 + 2 分钟冷却）。

## 4. 源码地图（internal/）

| 包 | 职责 |
|---|---|
| `webui/` | Wails 绑定层：`app.go`（通用+经典模式）、`express.go`（极速协调器）、`express_windows.go`（Windows 组装） |
| `session/` | 极速模式会话核心：状态机、注册事务补偿、房主心跳、身份自愈、成员刷新（前台 5s/托盘 30s） |
| `roomapi/` | Room API HTTP 客户端：幂等键、Retry-After 钳制、响应大小限制、SetupKey 防序列化 |
| `netbird/` | daemon gRPC 适配器（`rpc/` 为 protoc 生成）、版本检查、恢复监控、profile 管理 |
| `nbdaemon/` | NetBird MSI 安装/修复/移除：签名校验、UAC 提权、服务生命周期 |
| `securestore/` | DPAPI 加密存储：房间码、owner token、房间元数据（room.json），原子替换 |
| `natdetect/` | STUN NAT 类型探测（RFC 3489 简化版），给用户提供组网建议 |
| `updater/` | 热更新：版本比较、zip 下载（SHA256 校验）、解压（ZipSlip 防护） |
| `releasebuild/` | 嵌入 `netbird-release.json`（MSI URL/SHA256/ProductCode） |
| `n2n/` | 经典模式 edge.exe 进程编排 |
| `tap/` `nic/` | TAP 适配器安装/命名/等待；网卡查询 |
| `config/` | 客户端配置（config.yaml）与内置常量（`app_config.go`） |
| `observability/` | 日志脱敏（房间码/setup key/令牌/IP 模式） |
| `logger/` `diagnostics/` `security/` `platform/` `poll/` | 日志、诊断打包、经典模式配置加密、平台依赖、轮询工具 |

## 5. 构建与测试

```powershell
# 一键构建（自动下载并校验 NetBird MSI）
.\scripts\build-all.ps1
# 输出：build\bin\SoGame.exe、sogame-helper.exe、edge.exe、netbird_installer_0.74.7_windows_amd64.msi

# 客户端测试（主 module）
go test ./internal/...

# Room API 测试（独立 module；当前尚无测试文件，改动服务端时应补充）
cd server\room-api; go test ./...

# 安装包（需 Inno Setup 6）
.\installer\build-installer.ps1

# 热更新发布（生成 publish\update.json + zip，上传见 deploy/DEPLOY.md）
.\scripts\publish-update.ps1 -Version "2.2" -ReleaseNotes "..."
```

本地联调 mock：

```powershell
go run ./tools/room-api-mock/main.go   # 监听 127.0.0.1:9099，MOCK_RELAY_ENABLED 控制 relay 字段
# 客户端需把 Room API 地址切到 http://127.0.0.1:9099（UI 设置或 config.yaml 的 room_api_url）
```

## 6. 配置参考

### 6.1 客户端

配置文件：`%AppData%\SoGame\config.yaml`（0600 权限，保存时自动生成 `.bak`）。
字段：`mode`（classic/express，空=classic）、`room_api_url`、`express_nickname`、
`node_name`、`community`、`key`（AES 加密落盘）、`supernode`、`ip`。

**入口字段的空值语义与废弃迁移（2026-09-25 起）：**
`room_api_url` 与 `supernode` 以空值表示"跟随内置默认"（`omitempty`，默认值不落盘），
消费点统一在使用处回落到 `DefaultRoomAPIURL` / `DefaultSupernode`。
历史版本会把当时的默认值固化落盘，入口下线后残留值会导致全量报错（2026-09-24
明文 IP 下线事故的根因）。`config.MigrateDeprecatedEndpoints` 在 `LoadOrCreate`
所有加载路径（含 `.bak` 恢复）上把废弃名单中的旧值迁移为当前默认值并落盘；
`config.NormalizeSupernode` 同时应用于邀请码解码，使内嵌已下线节点的旧邀请码仍可用。
废弃名单在 `internal/config/app_config.go`（`deprecatedRoomAPIURLs` /
`deprecatedSupernodes`），**下线任何入口/节点时必须：改默认常量 + 把旧值加入名单 +
发版**，三步缺一不可。

内置常量（`internal/config/app_config.go`）：

| 常量 | 当前值 | 说明 |
|---|---|---|
| `DefaultRoomAPIURL` | `http://123.56.254.224` | 生产 Room API（**目前为明文 HTTP，TLS 化为已知待办**） |
| `DefaultSupernode` | `8.148.244.159:10090` | 经典模式默认中心节点（节点表见 `internal/n2n/edge.go` knownNodes） |
| `UpdateURL` | `https://virdy.cn/sogame/update.json` | 热更新 manifest |
| `STUNServerA` / `STUNServerB` | `stun.virdy.cn:3478` / `stun.l.google.com:19302` | NAT 探测 |

### 6.2 Room API 服务端（环境变量，`server/room-api/internal/config/config.go`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `NETBIRD_PAT` | **必填** | Management API 的 Personal Access Token |
| `ROOM_API_ENCRYPTION_KEY` | **必填** | setup key 落库加密密钥：base64(32B) 或 32 字符原文，其他取值按 SHA256 派生 |
| `NETBIRD_MANAGEMENT_URL` | `https://legengen.top` | NetBird Management 地址 |
| `ROOM_API_ADDR` | `:8080` | 监听地址（明文 HTTP，生产应置于 TLS 反代之后） |
| `ROOM_API_DB_PATH` | `room-api.db` | SQLite 路径（WAL 模式） |
| `ROOM_API_ADMIN_TOKEN` | 空（禁用管理端点） | 管理员禁用房间的令牌 |
| `ROOM_API_RELAY_ENABLED` | `false` | Relay 总开关（见 §6.3） |
| `ROOM_API_CREATE_RATE_PER_MINUTE` | 5 | per-IP 限流 |
| `ROOM_API_JOIN_RATE_PER_MINUTE` | 30 | per-IP 限流 |
| `ROOM_API_PEER_RATE_PER_MINUTE` | 60 | per-IP 限流 |
| `ROOM_API_MAX_BODY_BYTES` | 4096 | 请求体上限 |
| `ROOM_API_PROVISION_CONCURRENCY` | 2 | 建房并发信号量 |

改配置方式：**改环境变量 → 重启 room-api → 新创建/加入的房间生效**（无热加载）。

### 6.3 Relay 开关语义

- 开关由服务器掌握，随 Create/Join 房间响应的 `relay_enabled` 字段下发，客户端无权修改。
- 客户端在 enroll 时把该值持久化到本地 room.json，之后 Reconnect/Resume 读本地值，
  **不会重新拉取**——因此服务端改开关后，已保存的房间需离开并重新加入才能生效。
- `false`（默认）：纯 P2P 优先，中继连接不视为已连接，UI 提示"该服务器已关闭中继"；
  P2P 打洞失败时明确报告"无法建立 P2P 直连"。
- `true`：P2P 失败允许回退中继，状态机可进 `ConnectedRelay`，UI 显示"已连接 · 中继"。
- 注意：该开关只影响客户端状态机的路径判定与展示，不改变 daemon 的数据面行为；
  relay 真正可用还需 Management 侧存在可用的 relay 基础设施。

## 7. 改动时必须遵守的约定

**安全红线：**

- 房间码、setup key、owner token、PAT 不得进入日志；日志输出统一过 `observability.Redact`。
  `roomapi.SetupKey` 的 `String/Format/LogValue/MarshalJSON` 均返回 `[REDACTED]`，不要绕过。
- 服务端 SQL 必须参数化；owner/admin token 比较必须用 `subtle.ConstantTimeCompare`。
- MSI/更新包等外部产物必须先校验哈希与签名再执行；调用 `msiexec` 等系统程序用 System32 绝对路径。
- 提权操作一律走 `sogame-helper.exe` UAC 通道，参数经 `syscall.EscapeArg` 转义，拒绝拼接命令行。

**并发与生命周期：**

- `session.Service` 命令经 `beginCommand/endCommand` 互斥（30s 超时）；View 是高频只读路径，不得持锁调用外部服务。
- goroutine 必须有明确的 ctx 取消/退出路径（房主心跳、peers 轮询、daemon 事件订阅）。
- enroll 采用事务补偿模式：任何一步失败必须按逆序清理（daemon logout → 删除 profile → 清理本地凭证）。

**前后端契约：**

- Wails 绑定的方法签名变更后必须重新生成 `frontend/wailsjs/`（`wails generate module`），
  不要手工修补生成文件。
- Room API 请求/响应字段双侧保持一致；客户端对瞬态错误重试、永久错误不重试，
  服务端不得把内部错误与"资源不存在"混为同一 404。

## 8. 部署

- 热更新分发与 nginx 配置：`deploy/DEPLOY.md`（含 room-api 部署章节）。
- 生产事实：Room API 默认 `http://123.56.254.224`；Management 默认 `https://legengen.top`；
  更新分发在 `https://virdy.cn/sogame/`。

## 9. 许可证

SoGame 原创代码 AGPLv3（见 LICENSE）；第三方组件各自许可证不变
（NetBird daemon RPC BSD-3-Clause、NetBird MSI AGPLv3、n2n GPLv3、TAP-Windows GPLv2、
Wintun MIT、Go/npm 依赖各自许可证）。详见 NOTICE、TRADEMARK.md、THIRD_PARTY_LICENSES/。
修改源码的再分发需遵守 AGPLv3；分发 NetBird MSI 需遵守其许可证条款。

## 10. 文档地图

| 文件 | 内容 |
|---|---|
| `README.md` | 人类向项目介绍、安装与使用 |
| `deploy/DEPLOY.md` | 热更新与 room-api 部署运维 |
| `wireguard/README.md` | 极速模式叙述性介绍（与本文档互补，以本文档为准） |
| `docs/TAP_*.md` | 经典模式 TAP 适配器测试用例与回归记录 |
| `internal/netbird/rpc/README.md` | daemon.proto 生成代码说明 |
| `THIRD_PARTY_LICENSES/` | 第三方组件许可证 |

> 历史文档 `NETBIRD_FEATURE_AUDIT.md` / `NETBIRD_MINIMAL_ARCHITECTURE.md` /
> `NETBIRD_PHASE_B_REPORT.md` / `NETBIRD_RELAY_SWITCH.md` 已整合进本文档并删除，
> 需要时从 git 历史恢复。
