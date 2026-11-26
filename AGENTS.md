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
internal/                # 客户端全部业务逻辑（见 §4 源码地图）
frontend/                # React 前端（Vite + Wails 绑定，wailsjs/ 为生成代码）
server/room-api/         # Room API 服务端（独立 module）
installer/               # Inno Setup 打包（sogame.iss + build-installer.ps1）
scripts/                 # build-all.ps1（一键构建）、publish-update.ps1（热更新发布）
deploy/                  # DEPLOY.md + nginx/traefik 配置片段
tools/                   # room-api-mock（联调替身，127.0.0.1:9099）、
                         # update-keygen / update-sign（热更新 ed25519 签名工具）
docs/                    # 经典模式 TAP 测试记录、代码审计报告
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
   安装前校验 MSI 的 SHA256 + Authenticode 签名 + publisher CN/O + 证书指纹
   （`internal/nbdaemon/artifact_windows.go`）。
   **守护进程对用户不可见（一体化内建依赖）**：UI 无"安装/修复守护进程"概念——
   安装包 postinstall 静默预装；缺失/损坏/版本漂移时由 `EnsureDaemonReady`
   自动补齐（启动后台预检 → "待准备"提示条；用户点击或执行房间命令时触发，
   至多一次 UAC，60s 冷却 + 连续 3 次失败停手）。设计详见
   `docs/DAEMON_AUTO_SETUP_DESIGN.md`。
3. **凭证分级保护**：房间码与 owner token 经 Windows DPAPI（current-user）加密落盘
   （`internal/securestore/`）；服务端 SQLite 中 setup key 以 AES-GCM 加密、
   owner token 仅存 SHA256 哈希；房间码查询键为 HMAC-SHA256(pepper, code)
   （`server/room-api/internal/crypto.HashCode`，pepper 默认从主密钥域分离派生，
   旧版无盐 SHA256 数据在首次查询时自动在线迁移）。
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
  服务端看门狗回收超过 `ROOM_API_OWNER_OFFLINE_AFTER`（默认 5m）无心跳的房间
  （`teardownRoom`：删除 Policy/SetupKey、强制 DeletePeer 踢成员下线、删除 Group，
  终态 `closed` 并下发 410——成员客户端据此自动清理本地状态）。
  回收入口经 `store.BeginClosing` 状态 CAS（仅 active→closing）防并发复活；
  NetBird 删除类调用容忍 404（`netbird.ErrNotFound`，资源可能已被管理员在
  dashboard 手动删除）。从不心跳的 legacy 房间（旧客户端无心跳能力）不在回收之列，
  由客户端本地 24h 过期（`roomMaxAge`）兜底。
- 房主心跳对 `closing` 状态返回 **409**（而非 410）：teardown 失败回滚 active 后
  房间可继续服役，避免"房主收到 410 停跳→teardown 回滚→房主永不再跳"的自杀式回收链。
- 房主可解散房间（Close，owner token 鉴权）；成员收到 `room_closed` 后自动清理本地状态。
- 房主离线由服务端看门狗兜底；客户端另有身份自愈：进房必重置 daemon 身份，
  归属漂移/控制面死亡时自动以同一房间码重新 Join（`internal/session/repair.go`，
  4 次观测防抖 + 2 分钟冷却 + re-join 后 60s 漂移判定宽限期；stale 成员快照
  不参与漂移判定）。
- **服务端启动恢复（Reconcile）四阶段**：Default all-to-all policy 隔离自检（告警）→
  creating operation 收口 → closing 僵尸拨回 active 重排队（看门狗下拍回收）→
  creating/error 僵尸房间（超 10 分钟）清理 → stale 房间按 `OwnerOfflineAfter`
  阈值走 `teardownRoom` 回收（与看门狗同阈值同路径，不再是硬编码 30 分钟/Disable 语义）。
- **Default All-to-All 策略是隔离红线**：NetBird 账号默认的 Default 放行策略若
  enabled，所有房间数据面静默互通。新部署/重建账号必须执行一次
  `room-api -disable-default-policy`；启动自检发现仍会 enabled 时打审计告警。

## 4. 源码地图（internal/）

| 包 | 职责 |
|---|---|
| `webui/` | Wails 绑定层：`app.go`（通用+经典模式）、`express.go`（极速协调器）、`express_windows.go`（Windows 组装） |
| `session/` | 极速模式会话核心：状态机、注册事务补偿（自愈 repair 路径保留本地记录）、房主心跳、身份自愈、成员刷新（固定 5s；429 退避 15~60s 钳制；`Service.Close` 生命周期） |
| `roomapi/` | Room API HTTP 客户端：幂等键、Retry-After 钳制（429 档 60s）、响应大小限制、`ErrInvalidRoomCode` 格式哨兵、Enrollment 防序列化/防打印 |
| `netbird/` | daemon gRPC 适配器（`rpc/` 为 protoc 生成）、版本检查、恢复监控、profile 管理；`SetupKey` 类型（`String/Format/LogValue/MarshalJSON` 均 `[REDACTED]`） |
| `nbdaemon/` | NetBird MSI 安装/修复/移除：签名校验、UAC 提权、服务生命周期 |
| `securestore/` | DPAPI 加密存储：房间码、owner token、房间元数据（room.json，**元数据为明文 JSON**），原子替换 |
| `natdetect/` | STUN NAT 类型探测（RFC 3489 简化版）：候选池顺序故障转移（`config.STUNServerCandidates`）、共享 socket 两步对比判定（每次新建 socket 会把 Full Cone 误判为 Symmetric）、全不可达不武断判 UDPBlocked、原始错误只落日志不上屏；DNS 解析受 ctx 管辖。给用户提供组网建议 |
| `updater/` | 热更新：版本比较、manifest ed25519 验签（公钥内置时强制）、zip 下载（强制 SHA256、大小上限）、解压（ZipSlip/保留名/ADS 防护、目标目录先清理且拒绝符号链接） |
| `releasebuild/` | 嵌入 `netbird-release.json`（MSI URL/SHA256/ProductCode/证书指纹）；`helperSHA256Override` 经 ldflags 注入 |
| `n2n/` | 经典模式 edge.exe 进程编排：Wait goroutine 只等待自己启动的进程实例（代际计数防 Stop/Start 交替双回调）、stderr 采集带锁（Builder 非并发安全）、自动重启冷却期做代际检查（不用旧配置劫持新进程） |
| `tap/` `nic/` | TAP 适配器安装/命名/等待；网卡查询 |
| `config/` | 客户端配置（config.yaml）与内置常量（`app_config.go`） |
| `observability/` | 日志脱敏（房间码/setup key/令牌/IP/`-k` argv/32hex 密钥模式；`RedactingHandler` 已接入默认 slog） |
| `logger/` `diagnostics/` `security/` `platform/` `poll/` `syspath/` | 日志（写盘点统一强制 `observability.Redact`）、依赖检查、经典模式配置加密（AES-GCM v2）、平台依赖（PowerShell 一律环境变量传参+System32 绝对路径）、轮询工具、System32 系统程序路径共享 helper |

## 5. 构建与测试

```powershell
# 一键构建（顺序红线：先编译 sogame-helper.exe 算 SHA256，再 wails build
# 注入该哈希——主程序 UAC 提权前的 helper 完整性校验依赖它；
# 自动下载并校验 NetBird MSI；网络受限时用 -LocalMsiPath 指定本地 MSI）
.\scripts\build-all.ps1 [-LocalMsiPath <本地 MSI 路径>]
# 输出：build\bin\SoGame.exe、sogame-helper.exe、edge.exe、netbird_installer_0.74.7_windows_amd64.msi

# 客户端测试（主 module）
go test ./internal/...

# Room API 测试（独立 module。注意：rooms/store 等经 go-sqlite3 依赖 CGO，
# 需 CGO_ENABLED=1 且系统装有 gcc；本机 MinGW 已固化于 C:\mingw64，见 §8.1）
$env:CGO_ENABLED='1'; $env:CC='C:\mingw64\bin\gcc.exe'
cd server\room-api; go test ./...

# 安装包（需 Inno Setup 6）
.\installer\build-installer.ps1

# 热更新发布（生成 publish\update.json + zip，上传见 deploy/DEPLOY.md）
# 启用签名（推荐）：先 tools/update-keygen 生成密钥对并把公钥填入
# internal/config/app_config.go 的 UpdateManifestPublicKey，发布时加 -SignKeyFile
.\scripts\publish-update.ps1 -Version "2.2" -ReleaseNotes "..." [-SignKeyFile <私钥路径>]
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
| `DefaultRoomAPIURL` | `https://legengen.top` | 生产 Room API（traefik `legengen-rooms` 路由，HTTPS；明文 IP 入口已于 2026-09-24 下线） |
| `DefaultSupernode` | `8.148.244.159:10090` | 经典模式默认中心节点（节点表见 `internal/n2n/edge.go` knownNodes） |
| `UpdateURL` | `https://virdy.cn/sogame/update.json` | 热更新 manifest |
| `UpdateManifestPublicKey` | 空（未启用验签） | 更新 manifest ed25519 验签公钥（base64）；填入后 manifest 必须携带有效签名，见 §5 发布签名 |
| `STUNServerCandidates` | 自建 `legengen.top:3478`（netbird 内置 STUN，复用）+ 腾讯/小米/Cloudflare 公共 STUN | NAT 探测候选池（顺序即优先级，顺序故障转移；共享 socket 两步对比判定，不武断判 UDPBlocked） |

### 6.2 Room API 服务端（环境变量，`server/room-api/internal/config/config.go`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `NETBIRD_PAT` | **必填** | Management API 的 Personal Access Token（全账号权限，爆炸半径=整个 NetBird 账号） |
| `ROOM_API_ENCRYPTION_KEY` | **必填** | setup key 落库加密密钥：base64(32B) 或 32 字符原文，其他取值按 SHA256 派生（base64 合法但非 32B 直接报错） |
| `ROOM_API_CODE_PEPPER` | 空（自动派生） | 房间码 HMAC 查询键的 pepper；空时从加密密钥域分离派生，显式配置可独立轮换 |
| `NETBIRD_MANAGEMENT_URL` | `https://legengen.top` | NetBird Management 地址 |
| `ROOM_API_ADDR` | `:8080` | 监听地址（明文 HTTP，生产应置于 TLS 反代之后） |
| `ROOM_API_DB_PATH` | `room-api.db` | SQLite 路径（WAL 模式） |
| `ROOM_API_ADMIN_TOKEN` | 空（禁用管理端点） | 管理员禁用房间的令牌 |
| `ROOM_API_RELAY_ENABLED` | `false` | Relay 总开关（见 §6.3） |
| `ROOM_API_TRUST_PROXY` | `false` | 信任反代 X-Forwarded-For 时取**最右值**（单跳 traefik 追加语义；多跳代理或不追加真实 IP 的拓扑下严禁开启） |
| `ROOM_API_CREATE_RATE_PER_MINUTE` | 5 | per-IP 限流（429 带 `Retry-After`） |
| `ROOM_API_JOIN_RATE_PER_MINUTE` | 30 | per-IP 限流（同上） |
| `ROOM_API_PEER_RATE_PER_MINUTE` | 60 | per-IP 限流（同上） |
| `ROOM_API_MAX_BODY_BYTES` | 4096 | 请求体上限（<256 拒绝启动） |
| `ROOM_API_PROVISION_CONCURRENCY` | 2 | 建房并发信号量 |
| `ROOM_API_OWNER_OFFLINE_AFTER` | 5m | 房主无心跳多久后由看门狗回收房间（`<=0` 关闭看门狗） |
| `ROOM_API_OWNER_SWEEP_INTERVAL` | 1m | 看门狗扫描间隔 |

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

- 房间码、setup key、owner token、PAT、经典模式社区密钥不得进入日志。`internal/logger`
  在最终写盘点统一强制 `observability.Redact`（机制保证，不依赖调用点自觉；
  fallback 通道同样脱敏）；裸 slog 调用经默认 `RedactingHandler` 兜底；携带敏感值的
  error 不得把含 URL/参数的底层错误文本（如 `*url.Error`）内嵌进 `Error()`，根因一律走
  `Unwrap`。服务端审计日志只记录路由模板（`/rooms/:code/peers`），不记录原始路径，
  404 按 1/64 采样。`clientnetbird.SetupKey` 与 `roomapi.Enrollment` 的
  `String/Format/LogValue/MarshalJSON` 均 `[REDACTED]`/拒绝序列化，不要绕过。
  edge argv 含明文 `-k`：日志只允许打印 `BuildArgsForLogging`（同源脱敏版），
  绝不直接打印 `BuildArgs`。
- 服务端 SQL 必须参数化；owner/admin token 比较必须先哈希再 `subtle.ConstantTimeCompare`
  （消除长度时序侧信道）。
- MSI/更新包等外部产物必须先校验哈希与签名再执行；调用系统程序一律用
  System32 绝对路径（共享 helper `internal/syspath.System32`，覆盖
  msiexec/powershell/ping/tasklist/taskkill/netsh/pnputil/ipconfig/notepad
  全部调用点；Inno 脚本用 `{sys}` 展开）；PowerShell 脚本不做字符串拼接，
  外部数据一律经环境变量传入（`$env:SOGAME_*` 模式）。
- 提权操作一律走 `sogame-helper.exe` UAC 通道，参数经 `syscall.EscapeArg` 转义，拒绝拼接
  命令行；helper 的 `--log`/`--result` 写路径受白名单前缀约束（SoGame 数据目录/TEMP），
  且从白名单根到目标的每一级已存在父目录不得是 reparse point（防 junction 预置把
  提权写入重定向出白名单），已存在目标拒绝 reparse point，结果文件"临时文件+rename"
  原子发布。经典模式的 TAP/防火墙/网卡操作在主进程（requireAdministrator）内执行，
  属已知提权面，新增特权操作应优先放 helper。
- Wails 绑定方法若有两个返回值，第二返回值必须是 `error` 接口本身，不得是实现了
  error 的具体类型（如 `*ExpressError`）——typed-nil 会经桥接类型断言变成
  "非 nil 空错误"，成功结果被丢弃、前端 resolve(null)（回归测试
  `TestWailsBindingErrorContract` 机制化拦截）。
- 完整房间码只经 `ExpressRevealRoomCode` 按需获取：`ExpressState` 快照的 `roomCode`
  恒为空串，任何轮询/广播路径不得填充明文房间码。
- 守护进程是一体化内建依赖：UI 不出现"守护进程/安装/修复"概念；补齐动作只能经
  `EnsureDaemonReady`（BusyCommand=daemon-setup，60s 冷却+3 次失败停手，用户显式
  点击"立即准备"可清零重试；已有房间命令时等待至多 30s 而非覆盖 busy 位）；
  用户向文案一律称"极速模式组件/网络组件"。提权等待链 ctx 贯通（90s 预算对
  UAC/msiexec 阶段真实生效）。

**并发与生命周期：**

- `session.Service` 命令经 `beginCommand/endCommand` 互斥（30s 命令 ctx 超时；
  `beginCommand` 本身无看门狗）。View 是高频只读路径，不得持锁调用外部服务；
  room_closed 清理必须在命令互斥 + RoomID 二次校验下执行（不得在无锁 View 内
  forceClear）。`ExpressController` 替换/销毁 Service 实例前必须调用其 `Close()`
  （停房主心跳等后台 goroutine），`RepairService` 与房间命令共用 BusyCommand 互斥。
- goroutine 必须有明确的 ctx 取消/退出路径（房主心跳、peers 轮询、daemon 事件订阅）。
- enroll 采用事务补偿模式：任何一步失败必须按严格逆序清理
  （daemon logout → 删除 profile → tokens → metadata → codes）。
- 服务端 teardown 的落库（`MarkRoomClosed`/回滚 `SetStatus`）必须与外部 ctx 解耦
  （`context.WithoutCancel` + 短预算），防止 ctx 取消把房间卡进 `closing`。

**前后端契约：**

- Wails 绑定的方法签名变更后必须重新生成 `frontend/wailsjs/`（`wails generate module`），
  不要手工修补生成文件。
- Room API 请求/响应字段双侧保持一致；客户端对瞬态错误重试、永久错误不重试，
  服务端不得把内部错误与"资源不存在"混为同一 404；429/503 必须带 `Retry-After`。

## 8. 部署

- 热更新分发与 nginx/traefik 配置：`deploy/DEPLOY.md`（含 room-api 部署章节与
  `-disable-default-policy` 强制步骤、ed25519 签名发布指引）。
- 生产事实：Room API 与 Management 均经 `https://legengen.top`（traefik；
  明文 IP 入口 `http://123.56.254.224` 已于 2026-09-24 下线，仅作容器宿主存在）；
  更新分发在 `https://virdy.cn/sogame/`（上传目标仅 `/opt/sogame/downloads/sogame/`）。

### 8.1 依赖外部环境的事项清单（代码侧无法本地闭环）

> 以下事项的**代码/脚本部分**均已入库或可本地完成，但**生效动作依赖服务器、
> 生产拓扑或平台决策**。逐项完成后从本清单划除。对应审计发现编号见
> `docs/AUDIT_REPORT_2026-11-24.md`。

| # | 事项 | 依赖什么 | 状态 | 关联发现 |
|---|---|---|---|---|
| 1 | ~~coturn 部署~~（NAT 检测自建主服务器） | ~~virdy.cn 服务器资源 + 云安全组~~ | ✅ **完成（2026-11-26）**：决策**复用 netbird-server 内置 STUN**（原定 coturn 的 3478/udp 与之冲突，独立部署无必要）——候选池首位改为 `legengen.top:3478`（实测 Binding Request 正常），随客户端发版生效；期间公共 STUN 兜底无变化 | NAT 检测（2026-11-24） |
| 2 | ~~room-api 服务端修复的生产部署~~ | 服务器 | ✅ **完成（2026-11-26）**：4cb16fb 的审计修复（HMAC 房间码迁移、看门狗 CAS、限流前缀聚合等）已编译部署（二进制 773c56ed），端到端验证通过 | M-S1/M-S2 |
| 3 | ~~relay 两级开关对齐~~ | 生产 traefik 配置 | ✅ **完成（2026-11-26）**：决策"中继经域名 HTTPS 可用、IP 明文入口下线"——`config.yaml` 加 `relay.addresses`（Management 分发 `rels://legengen.top:443`），摘除 IP 明文 `/relay` 路由，`legengen-backend` 的 HTTPS relay 保留，`ROOM_API_RELAY_ENABLED` 保持 true | M-S3 |
| 4 | ~~全新部署冒烟~~ | 服务器 | ✅ **完成（2026-11-26）**：`prep-room-api.sh` 以临时 env 冒烟通过（密钥生成/幂等/字段齐全）；顺带修复脚本 CRLF 兼容问题（`.gitattributes` 已强制 `*.sh eol=lf`）。注：完整"从零起容器"验证仍待独立环境 | M-S4 |
| 5 | ~~IPv6 暴露确认~~ | 服务器网络拓扑 | ✅ **完成（2026-11-26）**：`legengen.top`/`virdy.cn` 均无 AAAA（纯 IPv4），限流前缀聚合（/64）暂不适用 | M-S2 前提 |
| 6 | **热更新验签启用**（代码侧完成） | virdy.cn 发布链 | 🔶 **代码已入库（2026-11-26）**：密钥对已生成，公钥已填入 `UpdateManifestPublicKey` 并随下版发布；**私钥离线保管（绝不可入库/上传）**。⚠️ 窗口期约束：公钥发版后所有 `update.json` 必须带 `-SignKeyFile` 签名，否则全量客户端更新被拒——下次发布必须同步签名 | H-3（上轮） |
| 7 | ~~更新入口 HTTPS 跳转~~ | virdy.cn nginx/traefik | ✅ **完成（2026-11-26）**：`sogame-updates` 80 明文入口 301 跳 HTTPS（实测）；域名 `/Download` 80 入口同加；IP 路由无证书不跳 | 前端/部署低危 |
| 8 | ~~traefik accessLog 红线~~ | 生产 traefik 配置 | ✅ **完成（2026-11-26）**：`RequestPath` + `RequestLine` 字段 drop（实测日志路径已抹除为 `-`），其余字段保留观测 | 服务端低危 F-9 |
| 9 | ~~CI 落地~~ | GitHub Actions | ✅ **完成（2026-11-26）**：`.github/workflows/ci.yml` 已入库（client vet+race、room-api CGO race），推送后首次运行需观察 | §7 流程风险 |
| 10 | ~~Docker 镜像 digest 固定~~ | 发布机可访问 registry | ✅ **完成（2026-11-26）**：Dockerfile 基础镜像 pin digest（`golang:1.25-bookworm` 与 `debian:bookworm-slim`，经服务器实际 pull 验证） | 上轮接受项 L-21 |

**本地环境固化说明**：MinGW-w64 GCC 16.2 已固化于 `C:\mingw64`（POSIX threads + UCRT，
winlibs 发行版）。room-api 测试与全量 race 门禁的本地执行方式：

```powershell
$env:CGO_ENABLED='1'; $env:CC='C:\mingw64\bin\gcc.exe'
go test -race -count=1 ./...                       # 主 module（D:\SoGame\SoGame）
cd server\room-api; go test -race -count=1 ./...   # 服务端 module
```

每版本发布前应全量执行（替代历史上"无法运行"的空档）。

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
| `docs/CODE_REVIEW_*.md` | 分支代码评审报告（按评审日期与分支命名） |
| `docs/AUDIT_REPORT_2026-11-12.md` | 全项目代码审计报告（含同日完成的修复记录与升级须知） |
| `docs/AUDIT_REPORT_2026-11-24.md` | 全项目代码审计报告·第二轮（守护进程一体化专项 + rooms 测试/race 首次真实运行 + B-1 桥接实证；修复项含 NAT 检测重构与 V-1 race 修复） |
| `internal/netbird/rpc/README.md` | daemon.proto 生成代码说明 |
| `THIRD_PARTY_LICENSES/` | 第三方组件许可证 |

> 历史文档 `NETBIRD_FEATURE_AUDIT.md` / `NETBIRD_MINIMAL_ARCHITECTURE.md` /
> `NETBIRD_PHASE_B_REPORT.md` / `NETBIRD_RELAY_SWITCH.md` 已整合进本文档并删除，
> 需要时从 git 历史恢复。
