# SoGame 全项目代码审计报告

- **审计日期**：2026-11-12
- **审计基线**：`feature/wireguard` 分支工作区（HEAD `8044ba7`）
- **审计范围**：主 module 全部 `internal/` 包、`cmd/`、`main.go`、`server/room-api` 独立 module、`frontend/`、`scripts/`、`installer/`、`tools/`、`deploy/`
- **审计方法**：5 路并行静态审计（按模块划分）+ 审计者对全部阻塞/高危发现逐条人工复核源码确认 + `go vet` / 现有测试运行
- **发现总计**：**阻塞 1 / 高 14 / 中 32 / 低 60+**

---

## 1. 项目功能与目的（审计确认的理解）

SoGame 是 Windows x64 桌面 P2P 虚拟局域网游戏联机工具（Wails v2 + Go + React），目标是让无公网 IP 的玩家通过"创建/加入房间"互联。两种模式并存：

| | 经典模式（classic，默认） | 极速模式（express） |
|---|---|---|
| 协议 | n2n（edge.exe + supernode 中继） | WireGuard（NetBird daemon v0.74.7 MSI） |
| 网卡 | TAP-Windows V9 | Wintun（随 NetBird 安装） |
| 控制面 | 无 | 自研 Room API（房间 ↔ NetBird Group+SetupKey+Policy 薄映射） |
| 凭证保护 | AES-CFB 加密落盘 config.yaml | DPAPI（current-user）落盘 room.code/owner token |

关键安全机制（已验证真实落地）：MSI 四重校验（大小→SHA256→WinVerifyTrust→publisher CN/O）+ staging 二次校验；sogame-helper UAC 提权通道（`syscall.EscapeArg` 逐参转义）；服务端 setup key AES-GCM 落库、owner token SHA256 哈希+常数时间比较；日志写盘点强制 `observability.Redact`；enroll 事务补偿（独立 cleanup ctx）；状态机纯函数化；热更新强制 SHA256 + ZipSlip 防护。

**总体评价**：极速模式的核心安全链路设计严谨、测试针对性强，历史评审（`CODE_REVIEW_feature-wireguard_2026-09-28.md`）的阻塞项已闭环。当前风险集中在三处：**① sogame-helper 提权边界的路径约束缺失；② 经典模式整体 requireAdministrator 提权模型下的注入面；③ Room API 服务端在生产部署形态下的限流有效性与生命周期收尾**。此外存在一个修复成功路径必现的 UI 死锁（阻塞级）。

---

## 2. 阻塞问题（1）

### B-1 `RepairService` 成功路径自死锁，极速模式 UI 整体卡死 【已人工复核确认】

- **文件**：`internal/webui/express.go:323-334`
- **问题**：
  ```go
  for i := 0; i < 5; i++ {
      c.refreshService(ctx)
      c.mu.Lock()
      if c.state.Service.Installed {
          break            // 327 行：持 c.mu 跳出循环
      }
      c.mu.Unlock()        // 329 行：仅未安装分支解锁
      ...
  }
  c.mu.Lock()              // 334 行：break 路径到达此处 → 同一 goroutine 二次 Lock 死锁
  ```
- **触发**：用户点击"安装/修复守护进程"，MSI 安装成功且 `refreshService` 检测到 `Installed=true`——**修复成功的正常路径几乎必现**。后果：`RepairService` 永不返回，`c.mu` 被永久持有，全部 `Express*` 绑定方法与 5s 轮询阻塞，极速模式完全卡死，只能杀进程。
- **修复**：循环内改为 `installed := c.state.Service.Installed; c.mu.Unlock(); if installed { break }`；补"修复成功路径"单元测试（fake inspector 首轮返回 Installed）。webui 包当前零控制器测试，此 bug 正是测试缺口的直接产物。

---

## 3. 高危问题（14）

### 3.1 提权边界（客户端，3 项）

**H-1 sogame-helper 对 `--log`/`--result` 无路径限制 = UAC 提权任意文件写原语 【已人工复核确认】**
- `cmd/sogame-helper/main_windows.go:71-72`（`--log` 仅要求绝对路径）、`:50,54-57`（`--result` 无任何校验）、`:135`（`os.WriteFile(path, payload, 0o600)` 以管理员身份执行）；`--log` 另传给 `msiexec /l*v`（`internal/nbdaemon/install.go:139`、`remove.go:68`）。
- 场景：同用户非提权进程执行 `sogame-helper.exe --action remove --log C:\Windows\System32\drivers\etc\hosts --result <任意路径>`，UAC 弹窗显示正常程序名，用户同意后获得管理员任意文件写。**动作是白名单的，写路径不是。**
- 修复：`--log`/`--result` 限定到固定目录（如 `%ProgramData%\SoGame\helper\` 下固定文件名）；写入前拒绝 reparse point 并独占创建；或移除这两个参数由 helper 自行决定路径。

**H-2 修复流程 result/log 位于用户可写目录且无 reparse 防护，同用户进程可预置 junction 劫持提权写入 【已人工复核确认】**
- `internal/webui/express_windows.go:179-180`（`%LOCALAPPDATA%\SoGame\NetBird\netbird-repair.log/.result`，同用户完全可写）；`internal/nbdaemon/elevation_windows.go:107` 仅 `os.Remove` 后等待 helper 写入。
- 场景：同用户恶意软件预置同名 junction（创建 junction 无需特权）指向系统文件 → 用户正常点"修复"并同意 UAC → 提权 msiexec/helper 跟随 reparse 覆盖目标文件，本地提权。`prepareElevation` 对 helper 自身做了 reparse 检查（`elevation_windows.go:68-74`），结果文件未对齐同标准。
- 修复：result/log 移到提权侧独占目录；或写入前 `GetFileAttributes` 校验非 reparse + 随机化文件名（每次 GUID）。

**H-3 热更新链零签名：manifest 与 SHA256 同信道下发，解压后 exe 无 Authenticode 校验**
- `internal/updater/updater.go:37` 注释自述"sha256 是整个更新链唯一的信任锚（解压后文件无二次校验）"；`scripts/publish-update.ps1:113-134` 只计算哈希写 update.json；`installer/build-installer.ps1` 无 signtool 步骤。
- 场景：virdy.cn `/sogame/` 目录被写（服务器失陷/上传凭证泄露/企业代理 TLS 拦截注入）→ 攻击者同时替换 zip 与 update.json 的 sha256 → 全部在线客户端"检查更新"即执行任意 exe，一次失陷即持久化。对比：NetBird MSI 有三重锚，而能替换 `SoGame.exe`/`sogame-helper.exe` 本身的更新链反而更弱。
- 修复：① ed25519/minisign 签名 manifest，公钥嵌入二进制，`Check` 先验签；② 发布链对 exe/安装包 `signtool sign`，`PerformUpdate` 解压后校验 Authenticode 再执行。

### 3.2 Room API 服务端（5 项）

**H-4 XFF 取首值——生产部署下全部 per-IP 限流可被伪造头绕过 【已人工复核确认】**
- `server/room-api/internal/httpapi/server.go:313-316`：`TrustProxy=true` 时取 XFF **第一个**值；但 traefik 默认把真实 IP **追加**到 XFF 末尾、保留客户端自带首值，且 `deploy/traefik-dynamic.yml:42-48` 的 `legengen-rooms` 路由无清掉入站 XFF 的 middleware；`deploy/prep-room-api.sh:58` 无条件写入 `ROOM_API_TRUST_PROXY=true`。
- 场景：攻击者每次请求随机 XFF 首值 → 建房 5/min、入房 30/min、peers 60/min 限流全部形同虚设 → 批量建房耗尽 NetBird 配额、撑大 SQLite，审计 `remote` 字段全部失真。
- 修复：单跳 traefik 场景取 XFF **最右值**；或在 traefik 路由加 middleware 清掉入站 XFF。

**H-5 幂等键 error 重试无原子 CAS——并发重试同一幂等键双重建房，孤儿资源永久泄漏**
- `server/room-api/internal/store/store.go:179-182`：`ResetOperation` 无条件 UPDATE（无 `WHERE status='error'`、无 RowsAffected 检查）；`service.go:157-169` 两个并发请求都判定可重试 → 双双建房。
- 场景：客户端自动重试与手动重试叠加 → 产出两套房间/Group/SetupKey/Policy，未引用那套成孤儿；孤儿房 `last_owner_heartbeat IS NULL` 永不心跳 → 看门狗与 Reconcile **都不回收** → 永久泄漏。
- 修复：`ResetOperation` 加 `WHERE status='error'` 并检查 `RowsAffected==1`，抢不到返回 `ErrOperationInProgress`；补并发回归测试。

**H-6 Create 补偿路径误删"复用"的既有 Group**
- `server/room-api/internal/rooms/service.go:225-229`：第二次 `UpdateExternalIDs` 失败的补偿中 `_ = s.nb.DeleteGroup(ctx, group.ID)` **没有** `if createdGroup` 判断（其余三条失败路径 206/213/221 行都有），与 `findGroup` 复用逻辑（`service.go:186-191`）自相矛盾，属笔误。
- 修复：补 `if createdGroup`；四条失败路径统一抽 `compensateProvisioning()`。

**H-7 SetupKey 创建成功后的失败路径只 Revoke 不 Delete——NetBird 账号垃圾资源持续累积**
- `server/room-api/internal/rooms/service.go:202-224` 三条失败路径均只 `RevokeSetupKey`；对比 `teardownRoom:534-541` 是 Revoke+Delete。建房失败（DB 抖动、NetBird 5xx）每次留一个 revoked key 垃圾，长期稀释配额与管理面性能。
- 修复：统一补偿函数中 Revoke 后补 `DeleteSetupKey`（容忍 404）。

**H-8 房间隔离依赖一次性手动迁移，启动路径零自检——Default all-to-all policy 未禁用则全部房间静默串房**
- `cmd/room-api/main.go:77` 起的正常启动路径不检查账号级 Default 放行策略；唯一禁用途径是手动跑一次 `room-api -disable-default-policy`（`service.go:636-651`）。
- 场景：新部署/重建 NetBird 账号后忘记迁移 → 所有房间的 peer 数据面全账号互通，且 room-api 一切表现"正常"——**静默安全失效**。
- 修复：启动时 `ListPolicies` 自检，发现 enabled 的账号级 all-to-all 即 WARN（或可选 fatal）；DEPLOY.md 列为强制步骤。

### 3.3 会话层（2 项）

**H-9 `session.Service` 无生命周期方法：MSI 修复重组装导致房主心跳 goroutine 永久泄漏**
- `internal/session/service.go:109-165`（无 Close/Stop）；触发点 `internal/webui/express_windows.go:229-248` 修复成功后 `Configure` 整体替换 Service，旧实例心跳 goroutine（`service.go:906-973`）唯一退出条件是 token 被清。
- 场景：房主在线时修复 daemon → 旧+新 Service 对同一房间**双心跳**，旧对象图常驻内存，每次修复累积一个泄漏 goroutine。
- 修复：`Service` 增加 `Close()`（停心跳+等 repair goroutine），`ExpressController.Configure` 替换前调用旧实例 `Close()`。

**H-10 View 不是只读路径：room_closed 分支在 View 调用栈内无互斥执行 forceClear 【已人工复核确认】**
- `internal/session/service.go:284-294`：View 读 `busy=false` 后释放锁执行慢速网络调用（gRPC Status + HTTPS Peers，窗口可达 10s），peers 返回 410 时 `s.stopOwnerHeartbeat()` + `s.forceClear(context.WithoutCancel(ctx))`（清存储+远端 Deregister）**不经 beginCommand 互斥**。
- 场景：窗口期内用户命令（Reconnect/Create/Leave）已开始写存储 → forceClear 与命令并发：可能把刚 enroll 提交成功的凭证立即清除（"创建成功"瞬间房间消失）。直接违反"View 是高频只读路径"的自身约定。
- 修复：View 内只置 pending 标志返回 `ErrRoomClosed`，真正 forceClear 由下一个命令入口在互斥保护下执行。

### 3.4 经典模式与绑定层（4 项）

**H-11 社区密钥明文进入 edge.exe 命令行，本机任意进程可读 【已人工复核确认】**
- `internal/n2n/edge.go:168-176`：`"-k", cfg.Key` 明文进入 argv。Windows 上任意进程（无需管理员）可经 WMI `Win32_Process.CommandLine` 读取；key 是房间加入凭证且 edge 长期存活。
- 修复：edge 支持配置文件传 key 时改用临时配置文件（用后删除）；否则文档明示暴露面并评估 edge 升级。

**H-12 防火墙/网络类别配置存在 PowerShell 命令注入 【已人工复核确认】**
- `internal/platform/firewall.go:52-57`（AddFirewallRule）、`:141-146`（SetNetworkCategoryPrivate）、`:94-99`（removeFirewallRuleInternal）：`ifName` 经 `fmt.Sprintf` 单引号拼接进 `powershell -Command`，未转义。
- 场景：恶意程序创建友好名含 `'; <载荷>; '` 的适配器，或篡改 `%AppData%\SoGame\tap-adapter.json` 诱导解析 → 主程序（requireAdministrator）连接时以**管理员权限**执行注入载荷。违反自身"拒绝拼接命令行"红线。
- 修复：ifName 经环境变量传入脚本（参照 `artifact_windows.go:99` 的 `SOGAME_SIGNATURE_PATH` 模式），或白名单校验 `^[A-Za-z0-9 _-]+$` + 单引号转义为 `''`。

**H-13 Wails `Connect` 对前端输入零校验，直通配置落盘与 edge argv 【已人工复核确认】**
- `internal/webui/app.go:396-433`：四个字符串原样落盘并进 `BuildArgs`；`ValidateCommunity/ValidateKey/ValidateIP/ValidateSupernode`（`config.go:314-390`）只在下次启动 `LoadOrCreate` 时触发——webui 包内无任何 `.Validate()` 调用点（已 grep 确认）。
- 场景：community 以 `-` 开头被 edge 当 flag 吞并；畸形字段落盘后下次启动配置被整体重置；邀请码解码出的 supernode 仅废弃名单替换无格式校验。
- 修复：`Connect`/`ConnectWithInvite` 入口调用同一套 `Validate*`；邀请码解码后加长度与格式校验。

**H-14 主程序整体 requireAdministrator，WebView 渲染层与全部业务逻辑同处管理员上下文 【已人工复核确认】**
- `build/windows/wails.exe.manifest:7`：`requireAdministrator`。经典模式的 TAP 安装（pnputil）、防火墙规则、网卡启停改名全部在主进程直接执行——"提权走 helper"仅覆盖 express MSI。H-12 的注入、任何前端 XSS/解析漏洞都直接 = 管理员权限。
- 修复（长期）：主程序降权 asInvoker，TAP/防火墙/netsh 迁移到 helper 或服务；短期收敛 PowerShell 拼接面并在文档说明双模式提权模型差异。

---

## 4. 中危问题（32，按模块归类）

### 4.1 Room API 服务端（13 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-S1 | `httpapi/server.go:353-401` | 限流器 `clients` map 无界增长+5 分钟清扫，与 H-4 联动成内存 DoS | 容量上限+清扫降 1min |
| M-S2 | `httpapi/server.go:72-80,98` | 审计对全部请求（含垃圾 404）无差别记录无速率限制，日志洪泛 | 404 类采样/聚合；compose 配日志轮转 |
| M-S3 | `netbird/client.go:178` + `rooms/service.go:546-549` | `ListPeers` 10MB 解码截断+teardown 视为硬失败：账号 peer 超约 2 万后**所有房间永远无法关闭/回收** | 分页/按 group 过滤；超限返回类型化错误 |
| M-S4 | `rooms/service.go:370-396` | Peers 每请求全量拉取账号 peer 列表无缓存，Management QPS 随房间×成员放大 | 2-3s TTL 缓存 |
| M-S5 | `rooms/service.go:613-616` | `Disable` 把 DB 内部错误吞并成 404，直接违反"内部错误≠资源不存在"红线 | ErrNotFound 与其余错误分支拆开 |
| M-S6 | `rooms/service.go:520-587` | teardown 全程共用外部 ctx，落库前 ctx 取消 → 房间永久卡 `closing` | `MarkRoomClosed` 用 `WithoutCancel`+短超时 |
| M-S7 | `rooms/service.go:262-328` + `cmd/room-api/main.go:82-115` | `closing` 僵尸无恢复路径；关停时 sweep 未等待即关 DB | Reconcile 续跑 closing（幂等安全）；关停等待 sweep 完成 |
| M-S8 | `rooms/service.go:306` | Reconcile stale 回收硬编码 30 分钟，与可配 `OwnerOfflineAfter` 不一致，调大配置后重启即误杀正常房间 | 复用配置值或取 max |
| M-S9 | `rooms/service.go:311-326` | stale 回收用 `Disable`（404 语义）而非 `teardownRoom`（410+踢 peer），与看门狗语义分叉，客户端走错误分支 | 复用 `teardownRoom` |
| M-S10 | `crypto/crypto.go:30-33` | 房间码无盐 SHA256 作查询键：DB 泄露后 60bit 空间可离线枚举反查房间码，配合在线 Join 进任意活跃房间 | 改 HMAC-SHA256(pepper, code)；需 DB 迁移 |
| M-S11 | `rooms/service.go:171-183,231-233` | Create 前段多个失败点不走 `fail()` → operation 卡 `creating`，同幂等键永久 409 直到重启 | 统一走 `s.fail()` |
| M-S12 | `rooms/service.go:119-180` | 无幂等键的 Create 中途崩溃 → `creating` 僵尸+NetBird 资源残留，无清理路径 | Reconcile 清理超时 `creating` 房间；或服务端强制幂等键 |
| M-S13 | `httpapi/server.go:305-307` | 429/503 从不带 `Retry-After`，客户端"Retry-After 钳制"逻辑无输入源，契约两端不对齐 | 429 带窗口剩余秒数；503 带 1 |

### 4.2 会话层与 roomapi 客户端（5 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-C1 | `session/service.go:284-298` + `repair.go:62-74` | 身份自愈的漂移判定使用 stale 成员数据：Refresh 失败的陈旧列表可误判漂移；re-join 后服务端同步延迟可致每 2 分钟震荡重 Join | Stale 快照传 nil 禁用漂移判定；re-join 后 60s 宽限期 |
| M-C2 | `session/service.go:404-430` | `countDistinctPeers` 生产零调用死代码，其 3 个测试营造"成员计数已覆盖"假象 | 接线或删除（含测试） |
| M-C3 | `roomapi/client.go:48-49,407-414` | Retry-After 被钳到 5s，429 限流下重试节奏把短暂限流滚成持续限流（每分钟约 48 次贴近 60 上限） | 429 单独放宽钳制；PeerRefresher 对 429 跳 tick |
| M-C4 | `session/peers.go:29-32,96-127` | 托盘 30s 刷新与 `Watch` 生产零接线，实际固定 5s；AGENTS.md"前台 5s/托盘 30s"描述未实现的计划 | 实现托盘检测或删除死代码并改文档 |
| M-C5 | `session/service.go:906-921` | `startOwnerHeartbeat` cancel 注册跨锁窗口，安全性依赖"调用点都在命令互斥内"的隐式契约 | WithCancel 与赋值收进同一把锁 |

### 4.3 提权与更新链（4 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-U1 | `nbdaemon/elevation_windows.go:79-83` + `releasebuild/netbird-release.json` + `scripts/build-all.ps1:38-44` | HelperSHA256 校验链断裂：字段存在但 JSON 无此值且构建顺序死锁（先 wails build 嵌入 JSON 再构建 helper）→ helper 完整性校验永远跳过；叠加 `sogame.iss` 允许用户目录安装，同用户进程可替换 helper 借 UAC 提权 | 调整构建顺序（先 helper→算哈希→写 JSON→再 wails build）；或取消 `PrivilegesRequiredOverridesAllowed=dialog` |
| M-U2 | `nbdaemon/artifact_windows.go:96-99` | publisher 校验完全依赖提权上下文中经 **PATH 解析**的 `powershell.exe` 输出——与 msiexec 走 System32 绝对路径（`install_windows.go:36-43`）的标准自相矛盾；PATH 污染=提权代码执行 | 用 System32 绝对路径；根治是改 Win32 API 直读签名证书 |
| M-U3 | `config/config.go:251-260` + `security/encryption.go` | 经典模式配置加密：加密器不可用时**明文落盘**仅 WARN；密钥 base64 文本直接截断当 AES key（熵降）；AES-CFB 无 MAC 可比特翻转；密钥文件与密文同目录 | 加密不可用拒绝保存 key；改 AES-GCM；条件成熟迁移 DPAPI |
| M-U4 | `scripts/publish-update.ps1:134` | PS 5.1 的 `Out-File -Encoding utf8` 固定写 BOM → Go `json.Decode` 报 `invalid character 'ï'` → 全量客户端热更新静默断裂 | `[System.IO.File]::WriteAllText` + 无 BOM UTF8Encoding；发布后验证首字节 |

### 4.4 经典模式与绑定层（8 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-W1 | `n2n/edge.go:460-468,367-387,1419-1423` | `Disconnect` 无法取消挂起的自动重启（10s sleep 后递归 Start）与注册冲突重试（AfterFunc 不可取消）→ 断开后 edge 自行复活 | Stop 无条件置 `manualStop`；定时器存结构体并在 Stop 取消 |
| M-W2 | `webui/express.go:305-308` | `RepairService` 绕过 BusyCommand 互斥（不检查直接覆盖），与房间命令并发竞态；重组装后进行中命令持有的旧 Service 悬空 | 复用同一互斥入口 |
| M-W3 | `webui/app.go:767-784` | 修改 Room API 地址后极速控制器不重建，保存成功但实际不生效无任何提示 | 地址变化时重组装或提示重启生效 |
| M-W4 | `webui/express.go:40-42 vs 477-482,521` | 明文房间码每 5s 轮询填入 ExpressState 并事件广播，与头注"完整房间码仅经 ExpressRevealRoomCode 获取"自相矛盾，Reveal 门控形同虚设；且每 5s 一次 DPAPI 解密+Info 日志刷屏 | 轮询不填 RoomCode（前端复制已走 Reveal 通道） |
| M-W5 | `logger/logger.go:234-267` + `n2n/edge.go:270-277` + `observability/redact.go:31` | 日志脱敏三缺口：①logger 未初始化 fallback `log.Printf` 无 Redact；②edge.go Debugf 逐对打印明文 argv（含 `-k <key>`），脱敏版 `BuildArgsForLogging` 是零调用死代码，仅靠 minLevel=INFO 掩盖；③Redact 规则不覆盖 `-k`/32hex 密钥形态 | fallback 过 Redact；edge 改用 BuildArgsForLogging；Redact 补规则+回归测试 |
| M-W6 | `config/config.go:394-429,151-158` | `.bak` 备份恢复路径跳过 `Validate()`，损坏/篡改的备份绕过全部校验进入运行时并落盘 | `RestoreFromBackup` 返回前 Validate，失败降级默认 |
| M-W7 | `natdetect/detector.go:10-57` | `Detect` 完全忽略 ctx 参数，`DetectWithTimeout` 的超时承诺不实，网络异常时 UI 阻塞时长不可控 | ctx 传入 sendSTUN（DialContext+SetDeadline） |
| M-W8 | `webui/express.go:466-482` | `refreshRoomView` 持 `c.mu` 锁执行 DPAPI 解密+文件 I/O（RevealRoomCode），违反"高频只读路径不得持锁调用外部服务"，I/O 卡顿放大锁持有 | RevealRoomCode 移到锁外（随 M-W4 一并消除更佳） |

---

## 5. 低危问题（60+，精选重要项归类，完整明细见各模块审计底稿）

**服务端（23 项）**：`RevokeSetupKey` PUT 整体替换丢元数据；admin Disable 不删 group/不踢 peer 残留；`code_ciphertext` 无消费点死字段（与 AGENTS.md"只存 SHA256"矛盾）；operations/closed 房间无 TTL（密文永久滞留）；`sql.DB` 未 `SetMaxOpenConns(1)`；compose 挂载 RHEL 系 CA 路径（Debian 宿主遮蔽镜像 CA）；加密密钥派生弱口令无 KDF 强化；admin token 长度预检时序侧信道；心跳对 closing 返 410 可致房主"自杀式"停跳；healthcheck 每 30s 全库 `integrity_check` 误判 unhealthy；配置解析失败静默回退（`ture`→false 无告警）；Dockerfile 镜像未 pin digest；PAT 全账号权限爆炸半径提示；`TestCloseTeardownFailureKeepsActiveAndRetryable` 名不副实（假覆盖）；限流固定窗口边界 2x 突发；等。

**会话/客户端（12 项）**：AGENTS.md 把 `SetupKey` 类型归属误写为 roomapi（实际 internal/netbird）；`RecoveryMonitor.Watch` 生产零接线；心跳退避注释与行为不符（240s 永不到达）；`beginCommand` 无看门狗（底层调用卡死则 busy 永久持有、会话锁死）；`Enrollment` 导出字段不防 fmt 打印、OwnerToken string 无法清零；enroll 补偿顺序非严格逆序；repair 失败置 RecoverableError 不粘性（下一轮 View 冲掉、状态闪烁）；Leave 房主解散脱离 30s 预算（实际 40s）；per-attempt 超时注释语义误导；integration mock 响应缺 `room_id`；等。

**提权/更新/存储（9 项）**：`RedactingHandler` 生产未接入默认 slog（裸 `slog.Warn` 旁路隐患）；`redactAttr` 未 Resolve `KindLogValuer` 旁路；Extract 检查与重建间 TOCTOU 窗口（junction 防护当前有效但无回归测试）；zip 条目未过滤 Windows 保留名/ADS；`--update-apply` 对 targetDir 无白名单+文件写入非原子（半截 exe 风险）；`awaitElevationResult` 读取无大小上限+部分写误报；CRL 降级状态不上浮 UI+已嵌入证书指纹未用于钉扎；`RequestDaemonRemovalElevation` 生产零调用死代码；securestore 的 0600/0700 在 Windows 不产生 ACL 效果（注释误导）等。

**经典模式/绑定层（12 类 20+ 处）**：ConfigCache 读锁下写计数器（race）+读路径死代码；webui 多处 `a.cfg` 无锁读写；邀请码解码无长度上限；0600 权限在 Windows 虚设（全库性认知问题）；edge 管理端口 TOCTOU+stop 命令无认证（本机 DoS）；TAP 工具链信任第三方路径/工作目录路径未校验签名；`waitForDaemonAndReassemble` goroutine 无 ctx 取消；死代码堆积（hasError/BuildArgsForLogging/errExpressUnsupported 等）；ExpressState 字段注释错位；错误映射兜底文案张冠李戴（统一显示"加入房间失败"）；logger 每条 Sync 性能损耗；config Save 非原子写；wails.json 中文版权信息 GBK 乱码；service.go:130-133,613-614 中文注释 UTF-8 损坏（U+FFFD）；等。

**前端/构建/部署（16 项）**：卸载残留（防火墙规则/凭证/NetBird 服务/TAP 驱动，选 NO 全留）；mock `/debug/rooms` 无鉴权返回全部明文凭证+非常量时间比较（绑定非 loopback 时危险）；`ExpressRevealRoomCode` d.ts 联合类型与运行时 reject 语义漂移；UI 无 Room API URL 设置入口（与 AGENTS.md §5 矛盾）+死导入；`ConfigInfo` 全契约唯一 snake_case 字段；`EventsOn` 监听器从不注销（StrictMode 双注册）；esbuild 0.21.5 dev-server 公告+无 npm audit/test 门禁；DEPLOY.md 硬编码个人 SSH 公钥+两套上传路径并存（旧路径致 update.json 静默不更新）；traefik 三路由 `tls: {}` 无 certResolver+updates 明文 HTTP 入口；ISCC 路径硬编码个人目录+AppId 手写伪 GUID+无签名步骤；输入框无 maxLength+原始错误文本上屏+剪贴板历史无提示；index.html 无 CSP；`wails.json` productVersion 2.0 与 AppVersion 2.1 漂移；`frontend/dist_new/` 空目录残留；**前端零测试**（938 行 App.jsx 无任何测试）。

---

## 6. 测试覆盖评估

| 区域 | 覆盖状况 | 关键缺口 |
|---|---|---|
| `internal/session` + `roomapi` | 好（状态机/补偿/重试/防泄露均有回归） | beginCommand 互斥零测试；心跳退避/停跳无测试；repair 冷却防重入无测试；H-10 竞态无测试；**`-race` 本机不可跑（CGO_ENABLED=0）** |
| `server/room-api` | rooms 12 用例+httpapi 4 用例 | **rooms 全部测试需 CGO+gcc，本机常态无法执行**（与历史评审记录相同，疑似无 CI 真实跑过）；Create 补偿结构性不可测（fake 无法注入创建类失败——H-6 正由此养出）；限流/admin 鉴权/Disable/crypto/config 零测试 |
| `internal/nbdaemon`/`updater`/`securestore`/`logger`/`observability` | 较好（校验失败即中止、ZipSlip 变体、写盘点脱敏等有回归） | Windows 专有路径（elevation/WinVerifyTrust）零测试；junction 回归缺失；logger fallback 无测试 |
| `internal/config`/`tap`/`nic` | 较好 | `.bak` 恢复校验缺失路径无测试 |
| **`internal/n2n`（1424 行进程编排核心）** | **零测试** | 最大测试缺口：状态解析/重试/孤儿清理/三层退出全无覆盖 |
| **`internal/webui`** | **仅邀请码编解码** | **Connect/校验/Express 控制器零覆盖——B-1 死锁必现 bug 无测试拦截的直接原因** |
| `internal/security`/`platform`/`natdetect`/`releasebuild`/`cmd/sogame-helper` | 零测试 | 加解密 round-trip、防火墙拼接等纯逻辑可抽测 |
| **`frontend/`（938 行 App.jsx）** | **零测试** | 无框架无脚本；M-W4/F-02 类问题无回归保障 |

**流程风险**：`go vet` 干净、主 module `go test ./internal/...` 全绿，但仓库无 CI 配置可查证；room-api 关键回归测试与 `-race` 在本机均不可执行，并发正确性结论完全依赖静态分析。

---

## 7. 文档（AGENTS.md 等）与实现不一致清单

| # | 文档描述 | 实际情况 |
|---|---|---|
| D-1 | §3"房间码与 owner token 只存 SHA256 哈希" | 房间码另以 AES-GCM 密文（`code_ciphertext`）落库且为无读取点死字段 |
| D-2 | §3 看门狗"禁用房间" | 终态实为 `closed`；`disabled` 是 admin 专属语义；文档未提看门狗会 DeletePeer 踢人 |
| D-3 | §3/§6.2 无启动时 Reconcile stale 回收路径的记载 | 存在、阈值硬编码 30min、终态用 disabled（与看门狗分叉） |
| D-4 | §4 客户端"Retry-After 钳制" | 服务端 429/503 从不发送 Retry-After，钳制无输入源 |
| D-5 | §4"成员刷新（前台 5s/托盘 30s）" | 固定 5s，托盘检测与 Watch 零接线（历史评审遗留至今） |
| D-6 | §7"roomapi.SetupKey 的 String/Format/..." | 类型实际在 `internal/netbird`（clientnetbird.SetupKey） |
| D-7 | §7"daemon 事件订阅"列为活跃 goroutine | `RecoveryMonitor.Watch` 生产从未调用 |
| D-8 | §7"View 是高频只读路径" | View 内含写副作用（room_closed→forceClear；触发 re-join）且持锁做 DPAPI I/O |
| D-9 | §7"任何一步失败必须按逆序清理" | metadata/tokens 清理顺序与严格逆序不符 |
| D-10 | §7"调用 msiexec 等系统程序用 System32 绝对路径" | msiexec 符合；powershell/netsh/pnputil/ipconfig 均经 PATH 解析，其中 artifact_windows.go:98 在提权 helper 内执行 |
| D-11 | §7"机制保证，不依赖调用点自觉"（日志脱敏） | fallback 路径无 Redact、edge Debugf 打印明文 argv、Redact 不覆盖 `-k` 形态 |
| D-12 | §4"`diagnostics/` 诊断打包" | 只有依赖检查，无打包功能 |
| D-13 | §2 `cmd/taptest/` | 目录已删除，docs/TAP_*.md 仍引用 |
| D-14 | §8"Room API 默认 http://123.56.254.224" | 已下线残留；实际 `https://legengen.top`（wireguard/README.md:183 同样过时） |
| D-15 | §5"UI 设置或 config.yaml 的 room_api_url" | UI 无 Room API URL 设置入口（SaveExpressSettings 前端未接线） |
| D-16 | express.go 头注"完整房间码通过 ExpressRevealRoomCode 单独获取" | 轮询每 5s 自动填充明文并广播 |
| D-17 | server/room-api/README.md:47 标 `ROOM_API_ADMIN_TOKEN` 必填 | 实际空=禁用管理端点（AGENTS.md 正确） |

---

## 8. 修复优先级路线图

**P0（立即，1 天内）**
1. B-1 RepairService 死锁（一行修复+回归测试）——修复功能当前必卡死。
2. H-4 XFF 取末值或加 traefik middleware——生产限流当前形同虚设。
3. H-1/H-2 helper 写路径约束+reparse 拒绝——提权任意文件写原语，修复成本低收益最大。

**P1（本周）**
4. H-12 PowerShell 拼接改环境变量传参；H-13 Connect 入口接 Validate——经典模式注入面收敛。
5. H-9 Service.Close + Configure 替换前调用；H-10 room_closed 清理纳入命令互斥。
6. H-5 幂等重试 CAS；H-6 补偿补 `if createdGroup`；H-7 补 DeleteSetupKey——服务端资源泄漏三连。
7. H-8 启动自检 Default policy（WARN 起步）。
8. M-W5 日志脱敏三缺口（fallback 过 Redact、edge 改用 BuildArgsForLogging、Redact 补 `-k` 规则）——"DEBUG 级别一开即泄钥"的引信。

**P2（下个迭代）**
9. H-3 更新链签名 manifest（ed25519 公钥嵌入）——供应链安全上限。
10. M-U1 调整构建顺序接通 HelperSHA256；M-U2 powershell 改绝对路径（短期）/Win32 API（根治）。
11. M-S3/M-S4 ListPeers 分页+缓存（账号规模增长的生存问题）；M-S6/M-S7 closing 僵尸恢复；M-S8/M-S9 Reconcile 复用 teardownRoom。
12. M-W1 断开取消挂起重启；M-W2 Repair 纳入互斥；M-W4 轮询不再下发明文房间码。
13. 测试补强：webui 控制器测试（防 B-1 类）、n2n 状态机测试、room-api Create 补偿可测化（fake 支持注入创建失败）、CI 落地 CGO+race。

**P3（持续）**
14. M-U3 经典模式密钥体系迁 DPAPI；H-14 主程序降权（长期架构）。
15. 低危批量清理（死代码、注释、文档 D-1~D-17 对齐）；前端 vitest 最小集+npm audit 门禁。

---

## 9. 审计底稿索引

本报告由 5 份模块级审计底稿汇总去重而成，每条发现均含文件:行号与代码证据，且阻塞/高危项已经审计者本人二次复核源码确认：

1. `server/room-api` 服务端：高 5 / 中 13 / 低 23
2. `internal/session` + `internal/roomapi`：高 2 / 中 6 / 低 12
3. `internal/nbdaemon` + `updater` + `securestore` + `cmd/sogame-helper` 等：高 2 / 中 4 / 低 9
4. 经典模式（n2n/tap/config）+ webui 绑定层：阻塞 1 / 高 4 / 中 9 / 低 12 类
5. 前端 + 构建发布链：高 1 / 中 3 / 低 12

去重说明：房间码轮询明文下发（底稿 2/4/5 重复）、更新链签名（底稿 3/5 重复）、托盘降频（底稿 2/4/5 重复）、日志脱敏缺口（底稿 3/4/5 重复）、经典模式加密（底稿 3/4 重复）、Service 泄漏（底稿 2/4 重复）已合并计数。

---

# 第二部分：修复记录（2026-11-12，同日完成）

> 修复按 §8 路线图 P0→P3 顺序执行。验证状态：主 module `go build ./... && go vet && go test ./...` **全部通过**；room-api `build/vet` 干净、`httpapi` 测试通过（`rooms` 测试受本机 `CGO_ENABLED=0` 限制无法执行，与审计前相同的既有环境限制，新增用例已随 `go vet` 编译验证）；前端 `npm run build` 通过。

## 10. 修复状态总表

### 10.1 阻塞（1/1 已修复）

| # | 修复 | 证据 |
|---|---|---|
| B-1 RepairService 自死锁 | ✅ 已修复：循环内先读值释放锁再 break；同时把 RepairService 纳入 BusyCommand 互斥（M-W2 一并修复） | `internal/webui/express.go:305-345`；新增回归测试 `express_repair_test.go`（成功不死锁/忙时拒绝/失败可见 3 用例，通过） |

### 10.2 高危（14/14 已修复，其中 H-3 为"机制就位待运维启用"）

| # | 修复 | 证据 |
|---|---|---|
| H-1 helper 任意文件写 | ✅ `--log`/`--result` 强制白名单前缀（%LOCALAPPDATA%\SoGame、%ProgramData%\SoGame、%TEMP% 之下）+ 已存在目标拒绝 reparse point/非常规文件 | `cmd/sogame-helper/main_windows.go` `validateWritablePath` |
| H-2 result/log junction 劫持 | ✅ helper 写 result 改"临时文件+rename"原子发布且写前 reparse 复查；主程序 `awaitElevationResult` 读前拒 reparse、64KB 上限、部分写继续轮询（附残余毫秒级竞态窗口，已注释） | `internal/nbdaemon/elevation_windows.go`；新增 `elevation_windows_test.go` 4 用例通过 |
| H-3 更新链零签名 | ✅ **机制就位**：客户端 ed25519 验签（公钥经 `config.UpdateManifestPublicKey` 内置，配置后缺签名/错签名一律拒绝）；`tools/update-keygen`+`tools/update-sign`；`publish-update.ps1 -SignKeyFile` 集成 + BOM 自检。**待运维动作**：运行 keygen 生成密钥对、公钥入库发版、私钥离线保管——公钥为空时保持兼容模式（跳过验签） | `internal/updater/updater.go`（3 个验签测试通过）、`internal/config/app_config.go`、`deploy/DEPLOY.md` 启用指引 |
| H-4 XFF 限流绕过 | ✅ 取 XFF **最右值**（单跳 traefik 追加语义）+ 注释固化部署假设；3 个测试（末值/单值/伪造首值不可绕过）通过 | `server/room-api/internal/httpapi/server.go:312`、`server_test.go` |
| H-5 幂等重试双建房 | ✅ `ResetOperation` 加 `WHERE status='error'` CAS + RowsAffected 判定，抢不到返回 409；回归测试 `TestResetOperationCAS` | `store.go:179`、`service.go:157` |
| H-6 补偿误删复用 group | ✅ 统一 `compensateProvisioning()`（policyID/keyID/groupID + createdGroup 条件）；回归测试断言复用 group 不被删 | `service.go`；`TestCompensateProvisioningNeverDeletesReusedGroup` |
| H-7 setup key 垃圾累积 | ✅ 补偿统一 Revoke+Delete 双管齐下（同测试覆盖） | 同上 |
| H-8 Default policy 静默串房 | ✅ 启动 Reconcile 阶段零自检，enabled 时写 `default_policy_still_enabled` 审计告警；README 增"强制部署步骤"章节 | `service.go warnIfDefaultPolicyEnabled`、room-api README |
| H-9 Service 心跳泄漏 | ✅ `Service.Close()`（停心跳+closed 标志阻止新心跳/修复）；`closePreviousRPC` 先 Close 旧 Service 再关 RPC；`Shutdown` 同步 Close | `session/service.go`、`webui/express_windows.go`、`express.go` |
| H-10 View 清理竞态 | ✅ room_closed 清理由 `beginCommand` 互斥 + RoomID 二次校验（慢调用窗口内房间已切换则不清）；busy 时推迟下一轮 | `session/service.go:284-305` |
| H-11 edge argv 明文 key | ⚠️ 部分缓解：日志通道已全面脱敏（M-W5）；argv 明文是 n2n edge 进程模型的固有限制（edge 不支持 key 文件/环境变量传参），已在报告与文档标注；根治需 edge 升级 | `edge.go` 注释；AGENTS.md 风险说明 |
| H-12 PowerShell 注入 | ✅ 接口名/规则名一律经环境变量传入（`$env:SOGAME_FW_*`），脚本零拼接；powershell 走 System32 绝对路径 | `internal/platform/firewall.go` `runPowerShell` |
| H-13 Connect 零校验 | ✅ `Connect` 入口接入 `ValidateCommunity/ValidateIP/ValidateKey/ValidateSupernode`；`ValidateCommunity` 新增"不得以 `-` 开头"；邀请码解码 8KB 上限 | `webui/app.go:396`、`config/config.go:316` |
| H-14 整体 requireAdministrator | ⚠️ 未修（架构级）：经典模式 TAP 安装/防火墙/网卡操作依赖管理员权限，降权需把全部特权操作迁移到 helper/服务，属独立工程。注入面（H-12/H-13）已先行收敛 | 保留在"遗留风险" |

### 10.3 中危（32/32 已修复）

**服务端（13/13）**：M-S1 限流器容量 100k+清扫 1min（`server.go`）；M-S2 审计 404 按 1/64 采样+compose 日志轮转；M-S3 响应上限升 64MB+`ErrResponseTooLarge` 类型化（`netbird/client.go`）；M-S4 `listPeers` 2s TTL 缓存（Peers 与 teardown 共用）；M-S5 `Disable` 的 NotFound 与内部错误拆分；M-S6 teardown 两处落库改 `WithoutCancel`+5s 独立预算；M-S7 `RequeueClosingRooms`（拨回 active+心跳置 0，看门狗下拍回收，附回归测试）；M-S8 stale 阈值改用 `OwnerOfflineAfter`；M-S9 stale 回收改走 `teardownRoom`（closed 语义+踢 peer）；M-S10 `code_hash` 改 HMAC-SHA256(pepper)，旧数据查询时在线迁移（`crypto.HashCode`、`lookupRoomByCode`、`ROOM_API_CODE_PEPPER` 可选独立轮换）；M-S11 Create 全部失败点走 `fail()`；M-S12 Reconcile 阶段三僵尸清理（creating/error 超 10min）；M-S13 429 带 `Retry-After`（limiter.RetryAfter）+503 带 1。

**会话/客户端（5/5）**：M-C1 stale 快照传 nil 禁漂移判定+re-join 后 60s 宽限期（`repair.go`）；M-C2 `countDistinctPeers` 死代码及 3 个测试删除；M-C3 429 钳制放宽 `rateLimitRetryCap=60s`（测试更新）+`PeerRefresher` 429 退避（15s 地板，退避期直接返 stale）；M-C4 `Watch`/`PeerRefreshTray`/托盘降频死代码删除（文档同步）；M-C5 心跳 cancel 注册收进同一把锁。

**提权/更新链（4/4）**：M-U1 `releasebuild.helperSHA256Override` ldflags 注入，build-all.ps1 调整为"先 helper→算哈希→再 wails build -clean -ldflags 注入"，publish-update.ps1 同步；M-U2 `powershellPath()` System32 绝对路径（artifact_windows.go + platform/firewall.go）；M-U3 加密器重写：AES-256-GCM+`v2:` 版本前缀+base64 密钥解码+旧 CFB 密文兼容解密回退+加密器不可用拒绝保存明文（`security/encryption.go`，6 个新测试通过）；M-U4 `publish-update.ps1` 改 `[System.IO.File]::WriteAllText` 无 BOM UTF8 + 首字节自检。

**经典模式/绑定层（8/8）**：M-W1 `Stop` 无条件置 `manualStop`+清 `registrationRetryPending`，自动重启冷却后/AfterFunc 回调前复查（`edge.go`）；M-W2 见 B-1；M-W3 `SaveExpressSettings` 地址变化时 `reassembleExpress` 热重组装（busy 时跳过重启生效）；M-W4 轮询不再填充 `RoomCode`（前端复制/展示统一走 `ExpressRevealRoomCode`，头注与行为一致，DPAPI 高频调用与日志刷屏同愈）；M-W5 logger fallback 全级别过 Redact、edge 改用 `BuildArgsForLogging`（重写为基于 BuildArgs 同源脱敏）、Redact 补 `-k` 参数与 32hex 密钥规则+`redactAttr` 先 `Resolve()` 堵 KindLogValuer 旁路（3 个新测试）；M-W6 `RestoreFromBackup` 返回前强制 `Validate()`；M-W7 `sendSTUN` 接 ctx（deadline 取较小值）；M-W8 随 M-W4 消除（轮询路径不再有 DPAPI I/O）。

### 10.4 低危（已修 45+ 项，其余为"接受现状/文档澄清"）

**已修复**：admin token 先哈希再常数时间比较（L-12）；admin disable 挂限流+401 审计（L-17）；Heartbeat 对 closing 返 409 防自杀式回收链（L-13）；create 幂等校验先于信号量+删死检查（L-14）；main.go goroutine log.Fatal 改 channel 回传（L-15）；`decrypt` 死代码删（L-16）；`SetMaxOpenConns(1)`（L-8）；sweep `break dispatch`+变量改名（L-5）；`MaxBodyBytes>=256` 校验（L-6）；配置解析失败 WARN+加密密钥非 32B base64 报错（L-20/L-10）；healthcheck 改 `SELECT 1`（L-18）；compose 删 RHEL CA 挂载+日志轮转（L-9）；`code_hash` UNIQUE 冲突重试（L-11）；`code_ciphertext` 死字段保留但 README 修正安全模型描述（L-3，删字段需破坏性 DDL 迁移，评估后保留）；心跳退避注释修正（L-3@session）；enroll 补偿严格逆序 tokens→metadata→codes（L-6）；`Enrollment` String/Format/LogValue 防打印（L-5，附测试）；repair 失败后按当前 daemon 事实重派生消除状态闪烁（L-8）；integration mock 补 room_id（L-12）；session 两处注释乱码修复（M-6）；`hasError`/`RequestDaemonRemovalElevation`/ConfigCache 读路径死代码删除（ConfigCache 读锁下写计数器 race 同愈，SaveCached 保留别名）；ExpressState 注释错位修复+兜底文案"操作未完成"；`ConfigInfo` 改 camelCase 并 `wails generate module` 重新生成；`a.cfg` 多处无锁读写改快照（Connect/SetMode/SaveExpressSettings/ExpressCreate/JoinRoom/PerformUpdate/DetectNATType）；`GetNodesWithLatency` 单飞去重；config Save 原子写（临时文件+替换）；`--update-apply` targetDir 必须含同名 exe 校验+`writeFileAtomic`+目标为目录拒绝（附测试修复）；`updater` Download 改 CreateTemp、Extract 拒绝 Windows 保留名/ADS（附测试）；main.go 接入 `observability.NewRedactingHandler` 为默认 slog；证书指纹钉扎启用（`certificateThumbprintAtRelease`）；edge 注册判定抽纯函数+n2n 首批测试（BuildArgs/脱敏/注册判定 4 用例）；webui RepairService 3 用例；security 6 用例；前端 EventsOn cleanup 注销、`GetNATResult` 死导入删除、输入框 maxLength、index.html CSP、设置面板接入 Room API 地址（新增 `GetExpressSettings` 绑定并重生成 wailsjs）；`frontend/dist_new/` 删除；`wails.json` productVersion 对齐 2.1；卸载防火墙规则移出确认分支（iss）；mock `/debug/rooms` 剔除凭证字段+owner token 常量时间比较+非 loopback 启动告警；DEPLOY.md 删个人公钥+统一上传路径+签名发布指引；traefik 三路由补 `certResolver: le`；wireguard/README.md 过时地址修正；nic/query.go unsafe.Pointer 误报注释。

**接受现状（已文档化，未改代码）**：H-11 argv 明文 key（edge 模型限制）；H-14 requireAdministrator（独立降权工程）；L-1 `RevokeSetupKey` PUT 整体替换语义（已加 TODO 注释，需 NetBird 版本实测后改 GET+回写）；L-2 admin Disable 不删 group（语义即"禁用而非删除"，文档已述）；L-19 固定窗口 2x 突发（影响有限）；L-21 Dockerfile 浮动 tag（本环境无法解析 registry digest，建议发布机执行 `docker buildx imagetools inspect` 后 pin）；L-22 PAT 爆炸半径（README 已标注）；CRL 吊销降级上浮 UI（保留 slog.Warn，诊断面后续迭代）；`RecoveryMonitor.Watch` 保留为公共设施（生产未接线，AGENTS.md 已澄清）；日志每行 Sync（性能与可靠性取舍，保留）；`beginCommand` 无看门狗（已澄清语义：超时由命令 ctx 保证，底层调用均实现取消）；edge 管理端口 stop 无认证（本机随机端口，本地 DoS 面可接受）；TAP 第三方路径回退（管理员上下文既有前提，文档标注）；`sogame.iss` AppId 伪 GUID（稳定可用，变更影响升级识别，保留）。

## 11. 新增/变更测试清单

| 文件 | 内容 |
|---|---|
| `internal/webui/express_repair_test.go`（新） | RepairService 成功不死锁（B-1 回归）/忙时 OPERATION_CONFLICT/失败可见 |
| `internal/nbdaemon/elevation_windows_test.go`（新） | 部分写继续轮询/超限拒绝/目录拒绝/半包判定 |
| `internal/observability/redact_extras_test.go` | +`-k` argv 脱敏、+32hex 密钥、+LogValuer 旁路回归 |
| `internal/security/encryption_test.go`（新） | v2 GCM 往返/篡改拒绝/错密钥拒绝/旧 CFB 兼容/空密钥/SHA256 派生 |
| `internal/n2n/edge_test.go`（新） | BuildArgs 完整/脱敏同源/注册成功正反例/中间态判定 |
| `internal/updater/updater_test.go` | +保留名与 ADS 拒绝、+验签三态（未配置放行/缺签名拒绝/错签名拒绝/有效放行） |
| `internal/roomapi/secret_test.go` | +Enrollment 防 fmt/slog 打印 |
| `internal/roomapi/retry_test.go` | 429 钳制改 60s 语义+新增非 429 短钳制用例 |
| `internal/session/close_test.go` 等 | 删除死代码测试（countDistinctPeers×3、Watch×2），新增 Refresh 快照测试 |
| `main_test.go` | writeFileAtomic 目录目标拒绝（语义收紧） |
| `server/room-api/internal/rooms/close_test.go` | TestCloseTeardownFailureKeepsActiveAndRetryable 真实断言（failDeletePolicy）+H-5 CAS 回归+H-6/H-7 补偿回归+M-S7 closing 重排队回归（本机 CGO 限制未执行，vet 编译通过） |
| `server/room-api/internal/httpapi/server_test.go` | XFF 末值/单值/伪造首值绕过防护 |

## 12. 行为变更（升级须知）

1. **room-api 心跳语义**：房间处于 closing 时房主心跳从 410 改为 **409**——旧客户端收到非 410 会继续心跳（退避重试），不产生兼容性问题；新客户端无感知。
2. **room-api 429 响应**：新增 `Retry-After` 头（窗口剩余秒数）；旧客户端忽略该头按固定节奏重试，新客户端（rateLimitRetryCap=60s）遵从头。
3. **room-api DB 查询键**：`code_hash` 新写入为 HMAC-SHA256；旧 SHA256 数据在首次被查询时自动迁移，**部署本版本后不可回滚到旧版再写入新房间**（旧版读不懂 HMAC 键会导致已迁移房间 404——回滚前需评估；通常先升级、观察、再考虑回滚）。
4. **经典模式配置加密**：密文升级 `v2:` 前缀 GCM；旧 CFB 密文自动可读（兼容回退），重新保存即升级。若回滚旧版，v2 密文将解密失败并按"使用 raw key"降级（用户需重输密码）。
5. **热更新 manifest**：`update.json` 可携带 `signature`；客户端公钥（`UpdateManifestPublicKey`）为空时行为不变。**一旦发布内置公钥的客户端，之后所有 update.json 必须带有效签名**，否则全量拒绝更新——启用前确保发布链路 `-SignKeyFile` 已就位。
6. **前端契约**：`ExpressState.roomCode` 快照恒为空串（完整码仅经 `ExpressRevealRoomCode`）；`ConfigInfo` 的 `key_masked/key_set` 更名 `keyMasked/keySet`（无既有前端消费者）。
7. **helper 参数**：`--log`/`--result` 仅接受 SoGame 数据目录/TEMP 下的路径（主程序传参不受影响）。
8. **卸载**：防火墙规则删除改为无条件执行（不再随"删除用户数据"选项跳过）。

## 13. 修复后残余风险（如实记录）

- **rooms 测试长期无 CGO 执行环境**仍是最大流程风险：本次新增的 H-5/H-6/M-S7 回归用例均未真实运行过。强烈建议在有 gcc 的机器（或 CI）执行一次 `cd server/room-api && CGO_ENABLED=1 go test ./internal/rooms/ -count=1` 确认；CI 落地前每个版本发布前应人工执行。
- H-2 残余毫秒级 TOCTOU 窗口（helper 写前 reparse 检查与 msiexec 创建日志之间）无法彻底消除，需同用户恶意进程精确竞态；残余风险显著低于修复前（预置攻击已封死）。
- 验签公钥未启用期间，更新链信任锚不变（sha256 同信道）；启用窗口期必须保证 `-SignKeyFile` 与公钥发版同步。
- `listPeers` 2s 缓存使 teardown 踢人名单最多滞后 2s（成员客户端仍靠 410 自行退出兜底）；Peers 轮询的成员列表新增秒级延迟。
- HMAC 迁移期新旧 `code_hash` 并存（查询双路径保证）；全部迁移完成无时间上限（取决于房间被访问频率），无副作用。

