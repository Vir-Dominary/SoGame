# SoGame 全项目代码审计报告

- **审计日期**：2026-11-24（第一部分：静态审计；第二部分 §11-14：同日 12:00 权限放开后的补充运行时验证）
- **审计基线**：`feature/wireguard` 分支工作区（HEAD `ba50d93`，工作区干净）
- **审计范围**：主 module 全部 `internal/` 包、`cmd/`、`main.go`、`server/room-api` 独立 module、`frontend/`、`scripts/`、`installer/`、`tools/`、`deploy/` 及 Wails v2.12.0 桥接层
- **审计方法**：6 路并行静态审计（5 路模块代理 + 审计者交叉复核）+ 阻塞/全部高危逐条人工复核源码与 Wails 运行库确认 + 实际运行 `go vet` / `go test` / `go test -race`（CGO+MinGW）/ 桥接实证程序验证
- **发现总计**：**阻塞 1 / 高 11 / 中 20 / 低 30+**（含补充验证新增 V-1）

---

## 1. 项目功能与目的（审计确认的理解）

SoGame 是 Windows x64 桌面 P2P 虚拟局域网游戏联机工具（Wails v2 + Go + React），让无公网 IP 的玩家通过"创建/加入房间"互联。两种模式并存：

| | 经典模式（classic，默认） | 极速模式（express） |
|---|---|---|
| 协议 | n2n（edge.exe + supernode 中继） | WireGuard（NetBird daemon v0.74.7 MSI） |
| 网卡 | TAP-Windows V9 | Wintun（随 NetBird 安装） |
| 控制面 | 无 | 自研 Room API（房间 ↔ NetBird Group+SetupKey+Policy 薄映射） |
| 凭证保护 | AES-256-GCM v2 加密落盘 | DPAPI（current-user）+ 服务端 AES-GCM/HMAC |

相对上一轮审计（2026-11-12，同日完成 1 阻塞 + 14 高 + 32 中的修复），其后仅有三个提交：`f669499` 守护进程一体化（去按钮化）、`a935f7d` 构建顺序调整、`ba50d93` 仓库清理。**本轮审计的双重点**：① 验证上轮修复真实落地（逐条抽查）；② 深挖 f669499 守护进程一体化新链路的质量与缝隙。

**总体评价**：上轮修复质量极高（全部抽查在位且多处带回归测试）；安全红线机制化程度持续领先同类项目（文案红线、脱敏红线均有测试锁定）。当前风险集中在四处：**① `ExpressRevealRoomCode` 的 typed-nil Wails 桥接陷阱使极速模式"分享房间码"主流程在所有发布版上失效（阻塞）；② 守护进程一体化新链路的预算/互斥/重组装协调存在系统性缺口（11 条高危中 5 条属此）；③ 经典模式 n2n 进程编排的并发正确性债（上轮 M-W1 修复存在两条残余路径）；④ 三个构建/发布脚本对 helper 的编译 flags 互不相同，破坏 helperSHA256Override 校验链。** 服务端 room-api 经上轮修复后契约兑现度高，本轮仅存量逻辑竞态与限流绕过问题。

---

## 2. 阻塞问题（1）

### B-1 `ExpressRevealRoomCode` 经 Wails 桥接后成功路径 resolve(null)——房间码永不显示，极速模式邀请功能在发布版断裂 【已人工复核确认（含 Wails 运行库源码级验证）】

- **文件**：`internal/webui/app.go:938`（签名）+ `internal/webui/express.go:306,329`（成功返回 `(*ExpressError)(nil)`）+ `frontend/src/App.jsx:328,708`（消费方）
- **根因**：绑定方法签名为 `func (a *App) ExpressRevealRoomCode() (string, *ExpressError)`——第二返回值是**具体类型**而非 `error` 接口。Wails v2.12.0 桥接链逐级放大 typed-nil 陷阱：
  1. `internal/binding/boundMethod.go:103`：`callResults[1].Interface().(error)`——成功路径的字面 `nil` 是 typed-nil（`*ExpressError`），装箱成 interface 后非 nil，断言 `error` 成功，`err != nil` **恒为真**；
  2. `internal/frontend/dispatcher/calls.go:51-59`：`err != nil` 分支**丢弃 Result**（房间码被丢），置 `Err = err.Error()`；而 `ExpressError.Error()` 对 nil receiver 返回 `""`（express.go:100-105）；
  3. `frontend/runtime/desktop/calls.js:180`：`if (message.error)` 空串 falsy → **`resolve(null)`**。
- **影响**：前端 `App.jsx:328` `.then(code => { if (code) ... })` 中 code=null → 房间码区域（:708 依赖 `expressRoomCodeRevealed`）永不渲染；catch 也不触发（是 resolve 而非 reject），**完全静默**。房主创建房间后无法展示/复制完整房间码 → 无法邀请任何人——极速模式核心价值链断裂。失败路径同样损坏：reject 的是 `e.Code`（英文错误码字符串）而非可读 Message。d.ts 声称 `Promise<string|app.ExpressError>`（App.d.ts:31）与运行时语义三重漂移。
- **注意**：会话层 `session.RevealRoomCode`（service.go:425-441，DPAPI+clearBytes）本身正确；问题仅在用户向 API 的签名约定。
- **修复**：改为 `ExpressRevealRoomCode() (string, error)`（内部把 `*ExpressError` 包装为普通 error），执行 `wails generate module` 重新生成；**机制化防御**：加契约测试用反射断言"所有 Wails 绑定方法第二返回值必须是 `error` 接口类型"——同类 typed-nil 问题在全部 40+ 绑定方法上机械性绝迹（当前全部 `*ExpressError` 返回方法中仅此一个是双返回值，其余以 `ExpressState` 携带错误，恰是本 bug 只有一个的原因，但没有任何机制保证未来新增方法不踩坑）。

---

## 3. 高危问题（11）

### 3.1 守护进程一体化新链路（5 项，f669499 引入或与其直接相关）

**H-1 daemon-setup 的 90s 总预算对 UAC/msiexec 阶段完全失效：`launchElevation` 硬编码 `context.Background()`，busy 位最长可被占用 10 分钟 【已人工复核确认；2 路代理独立发现】**
- `internal/webui/express.go:443-447`（90s `setupCtx` 调 repair）→ `express_windows.go:197-231`（repair 闭包函数体不消费 ctx）→ `elevation_windows.go:107,125`：`launchElevation` 不接收 ctx，内部 `awaitElevationResult(context.Background(), resultPath)`，实际超时为 `elevationResultTimeout = 10 * time.Minute`（:44）。
- **影响**：msiexec 卡死（杀软扫描）或用户挂起 UAC 弹窗时，`BusyCommand="daemon-setup"` 最长占用 10 分钟，期间**全部**房间命令（含 Leave/Disconnect 本地清理）被"已有房间操作正在进行"拒绝；Create/Join 的 Wails promise 同步挂起最长 10 分钟。直接违背 `express.go:509`"断开/离开等本地清理命令必须总能执行"的注释意图与设计文档 DAEMON_AUTO_SETUP_DESIGN.md:144 的 90s 承诺。
- **修复**：ctx 贯通 `RequestInstallerElevation`/`launchElevation`/`awaitElevationResult`（超时取 `min(自身上限, ctx 截止)`）；Leave/Disconnect 对 `daemon-setup` busy 放行（本地清理不依赖 daemon）。

**H-2 `waitForDaemonAndReassemble` 不检查 BusyCommand 即重组装，可杀死进行中的房间命令与自愈事务**
- `express_windows.go:281-300`（后台 goroutine 拨号成功即 `assembleWindowsExpress`）+ `:165-181`（`closePreviousRPC` 无条件 `oldRooms.Close()`+关旧 RPC），而 `reassembleExpress` 注释明确约定"调用方负责确保当前无房间命令进行中"（:144-145）。竞态路径：daemon-setup 成功后旧 gRPC 连接自动重连，`daemonHealthy` 经旧 probe 提前返回 true → Create/Join 开始 → 后台重组装的下一次拨号在命令 30s 窗口内成功 → 旧 service/RPC 当场被关。
- **影响**：命令中途 RPC 失败 → `RecoverableError`；补偿链用已关闭 adapter 执行（CleanupFailures）；Create 场景服务端遗留孤儿房间（房主从未心跳，看门狗不回收 never-heartbeat 房间）；即使命令侥幸完成，新 service 因 `SetResumePending(true)`（:123-126）把刚进的房间退回"待恢复"。
- **修复**：`assembleWindowsExpress` 前检查 `GetState().BusyCommand`，非空则推迟重试；补并发回归测试。

**H-3 用户在房间内改 Room API URL 即触发"心跳停止 + 回退待恢复"，5 分钟后房间被服务端看门狗解散**
- `app.go:843-851`（URL 变化且 controller busy 为空即 `reassembleExpress`）→ `closePreviousRPC` 停旧心跳 → 新 service 因 `metadata.Load()` 成功置 `resumePending=true`（express_windows.go:123-126），**心跳不会自动恢复**（仅 Reconnect 命令重启心跳，service.go:492-494）。互斥检查另有盲区：session 层身份自愈占用的是 session 层 busy（repair.go:116-119），controller 层 `GetState().BusyCommand` 看不到，重组装可撞上进行中的 re-Join 事务。
- **影响**：房主 merely 修改 Room API 设置 → UI 回退"检测到上次的房间"提示框、心跳静停；用户不点"恢复"则服务端 `OwnerOfflineAfter`（默认 5m）无心跳即 `teardownRoom`——房间被解散、全员被踢。用户视角："改了个设置导致房间被拆"。
- **修复**：重组装时旧实例已连接则继承 `resumePending=false` 或完成后自动恢复心跳；重组装前置检查 session 层 busy（暴露 `Service.Busy()`）。

**H-4 `performDaemonSetup` 无条件覆盖 `BusyCommand`，与进行中的房间命令互踩；设计文档承诺的"等待 30s"未实现 【2 路代理独立发现】**
- `express.go:426`（不检查直接 `c.state.BusyCommand = "daemon-setup"`）+ `:563`（房间命令收尾无条件置空，会把 daemon-setup 标志一并清掉）。设计文档 DAEMON_AUTO_SETUP_DESIGN.md:119 明确要求"若已有房间命令在跑，等待其完成（最多 30s）再执行"——代码与测试（express_daemon_test.go）均未覆盖。
- **影响**：Create/Join 进行中（busy=create）并发触发 `ExpressPrepareDaemon`（前端 3s 轮询的 stale busy 使按钮仍可点击）→ busy 被覆盖 → msiexec 与 enroll 特权事务真实并发、互斥语义破损、前端"准备中"提示条提前消失。直接违反"RepairService 与房间命令共用 BusyCommand 互斥"的自身红线。
- **修复**：占位前检查非空返回 `expressErrOperationConflict` 或实现等待；命令收尾仅清空自己的命令名。

**H-5 helper 提权写路径只校验最终目标、不校验中间目录的 reparse point——上轮 H-1/H-2 修复的边缘缺口，管理员跨用户定向写原语仍存**
- `cmd/sogame-helper/main_windows.go:134-192`：`validateWritablePath` 词法白名单匹配后**仅对最终目标文件**做 reparse 检查（:153），`rejectReparsePoint` 对不存在路径直接放行（:171-173）；`ensureLogDirectory` 的 `MkdirAll` 对已存在 junction 目录视为成功（:123-125）。**从未检查中间目录**（`%ProgramData%\SoGame`、`%LOCALAPPDATA%\SoGame` 本身）。junction 创建无需特权。
- **攻击场景**：标准用户 B 在安装前预置 `mklink /J C:\ProgramData\SoGame <任意目录>` → 管理员安装时 Inno postinstall 以 `--log {commonappdata}\SoGame\netbird-install.log` 调用 helper（sogame.iss:116）→ 白名单词法通过 → msiexec 以管理员向 B 指定目录写详细日志（跨用户读 + 定向写）；运行时同理经 `%LOCALAPPDATA%\SoGame\netbird-repair.log/.result`（express_windows.go:208-209），叠加硬链接（`mklink /H`）可升级为管理员任意文件内容覆盖（内容为 MSI 日志/结果 JSON）。
- **修复**：从白名单根到目标**逐级父目录** Lstat+GetFileAttributes 查 `FILE_ATTRIBUTE_REPARSE_POINT`；或 log/result 收束到程序自建且 ACL 明确的子目录（首次创建时校验目录链）。

### 3.2 提权与更新链（2 项）

**H-6 热更新解压目录持续用户可写且被提权子进程消费——TOCTOU 本地提权，与"签名信任层"无关的执行层竞态**
- `app.go:682,695-700`（zip 解压到固定用户可写目录 `%TEMP%\sogame-update`，随后以管理员令牌 `exec.Command(newExe, "--update-apply", ...)`）+ `main.go:122-156,196`（`applyUpdateFiles` 直接 ReadFile 无二次哈希校验）+ `main.go:144`（`waitExecutableUnlocked` 可阻塞 60s，把竞态窗口拉宽到数十秒）+ `main.go:152-153`（完成后自动以管理员启动复制产物）。
- **影响**：SoGame 默认管理员运行，同账户 medium 进程在解压后的任意时点（尤其 60s 等锁窗口）替换 `%TEMP%\sogame-update\SoGame.exe`/`sogame-helper.exe` → 提权子进程把恶意文件复制进安装目录并自动管理员启动。**关键**：即便未来启用 ed25519 验签，签名校验的是下载时的 zip，消费的却是解压后持续可变的目录——窗口依旧存在（与已接受的"sha256 同信道信任锚"是不同层面的问题）。
- **修复**：staging 目录改仅管理员可写 ACL（`ProgramData\SoGame\staging` + 显式 DACL）；或 `runUpdateApply` 复制前整体重验（把期望哈希传给子进程重新比对）。

**H-7 提权上下文以裸名执行系统程序共 12 处，违反自家"System32 绝对路径"红线——其中 2 处构成现实提权链**
- `webui/app.go:560`（`exec.Command("notepad.exe", ...)`，主程序 requireAdministrator 下经 PATH 解析）+ `sogame.iss:134`（卸载器 `Exec('netsh', ...)`，Inno 的 Exec 对裸名做 CreateProcess 完整搜索**含当前目录**，卸载器运行于 %TEMP% 随机目录——同用户进程可预置 `netsh.exe` 借管理员卸载执行任意代码）+ `artifact_windows.go:114`（`powershellPath()` 回退分支返回裸名 `"powershell.exe"`，形式违规）；另 10 处非提权敏感但同属红线未兑现：`edge.go:999,1306,1424`（ping/tasklist/taskkill）、`platform/tap.go:209,233,243,252,378,384`（netsh/pnputil）、`diagnostics/dependencies.go:113`（ipconfig）。
- **影响**：用户级已沦陷场景下（PATH 目录放置同名 exe）借 SoGame 管理员上下文完成提权。`firewall.go:37-45` 的 `powershellPath()`（System32 绝对路径+存在性回退）已是正确示范，其余调用未跟进；`install_windows.go:36-43` 的 `msiexecPath()` 亦合规——同一红线两重标准。
- **修复**：仿照 `powershellPath()` 为全部系统程序生成 `%SystemRoot%\System32\...` 绝对路径；iss 改 `Exec(ExpandConstant('{sys}\netsh.exe'), ...)`。

### 3.3 经典模式与配置（3 项）

**H-8 密钥解密失败时密文残留必然超过 ValidateKey 上限，触发用户配置被静默整体重置——房间身份丢失 【已人工复核确认】**
- `config/config.go:168-177`（解密失败 WARN 后**保留密文** "using raw key"）→ `:189` `Validate()` → `ValidateKey`（:370，len>64 拒绝）→ `:198-204` `.bak` 恢复（同样含密文同样失败）→ `DefaultConfig()`（**随机 community**）覆盖落盘。加密格式 `"v2:"+base64(nonce+sealed)` 对 17+ 字符 key 必然超 64。
- **触发条件现实**：`.key` 文件丢失/重装系统 → `GetOrCreateEncryptionKey` 自动生成新密钥（security/encryption.go:153-166）→ GCM 认证失败 → 密文残留 → 校验失败 → 静默重置。**用户房间身份与密钥无声消失**，无任何用户可见提示。
- **修复**：解密失败时置 `cfg.Key = ""`（或对密文形态跳过长度校验单独降级），绝不把密文当明文参与校验；补失败路径测试。

**H-9 edge Wait goroutine 引用 `e.cmd` 字段而非局部 `cmd`——Stop/Start 快速交替后对错误的进程调用 Wait，双回调、误删 PID 文件与并发双重启 【已人工复核确认】**
- `edge.go:340-351`：`done`/`pid` 已捕获为局部变量，goroutine 却对字段 `e.cmd.Wait()`。时序：进程 A 退出但 Wait goroutine A 未调度 → 用户 Stop 完成、Start(B) 把 `e.cmd` 替换 → goroutine A 恢复后**等待 B 退出**；B 退出时 goroutine A 与 B 的 goroutine 同时进入后续逻辑（:357-429）：双份回调、双份自动重启判定；goroutine A 的 `clearEdgePID()`（:355）还会删掉 B 刚写的 PID 文件使孤儿清理失效。"断开后立即重连"是常见操作序列。
- **修复**：goroutine 内改用局部 `cmd`，退出路径只处理自己启动的进程。

**H-10 自动重启 10s 冷却期内用户手动 Connect，冷却到期后用旧配置劫持停止新进程——上轮 M-W1 修复的残余路径**
- `edge.go:383-394`：冷却到期只检查 `manualStop`，不检查是否有新进程；有则 `e.Start(cfgA)` → Start 检测到 B 在运行（:209-218）→ **Stop 掉用户刚建立的 B，用崩溃前旧 cfg 重启 C**；用户重连时修改过的房间参数被回退。
- **修复**：`Start()` 成功时递增 generation 计数，冷却回调比较实例代际/`e.cmd` 是否仍是退出时的实例，否则放弃重启。

### 3.4 构建与发布链（1 项）

**H-11 三个构建/发布脚本对 sogame-helper.exe 的编译 flags 互不相同，`build-installer.ps1` 的"自动补编译"路径必然破坏 helperSHA256Override 完整性校验链 【已人工复核确认；2 路代理独立发现】**
- `scripts/build-all.ps1:52`（`go build -o ...` 无 flags）vs `installer/build-installer.ps1:37`（`-trimpath -ldflags "-s -w"`）vs `scripts/publish-update.ps1:90`（`-tags "production" -ldflags "-s -w -H windowsgui"`）——**三个互不相同**；而 publish-update.ps1:86-88 注释自我声明"两脚本的 helper 构建 flags 必须一致，否则注入值与发布产物不匹配"。
- **触发链**：`wails build -clean` 清空 `build\bin` 只生成主程序 → 直接跑 `build-installer.ps1` → 自动补编译 helper（a2a34e2 引入，flags 不同 → 二进制必然不同 → SHA256 ≠ SoGame.exe 内注入的 `helperSHA256Override`）→ 安装后任何极速模式操作 → `prepareElevation`（elevation_windows.go:80-84）digest 比对失败 → 提权被拒 → 3 次失败停手 → **极速模式全挂且提示仅为"准备多次失败"，无任何指向线索**。讽刺：只有走了正式发布注入链的安装包才踩雷，手工构建反而跳过校验。
- **修复**：三处 flags 统一（提取共享片段）；补编译路径打印显式警告并输出哈希供比对。

---

## 4. 中危问题（20，按模块归类）

### 4.1 Room API 服务端（4 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-S1 | `rooms/service.go:699-706` + `store/store.go:255-262` | **看门狗旧快照竞态**：`BeginClosing` CAS 只守卫状态不守卫"过期前提"。看门狗取得快照后（`ownerSweepBudget=30s` + 4-worker 队列允许最长 30s 延迟），房主心跳到达且成功，worker 仍持旧快照 CAS（此刻仍 active）→ 刚恢复心跳的在线房间被完整回收（成员全踢、终态 closed）。`TestTeardownDoesNotResurrectClosedRoom` 只覆盖"已 closed"场景 | CAS 单语句合并过期复查 `WHERE id=? AND status='active' AND last_owner_heartbeat<=?`；补"心跳续命 vs 旧快照"回归测试 |
| M-S2 | `httpapi/server.go:350-364` + `store/store.go:87-112` | **IPv6 多前缀轮换绕过全部 per-IP 限流**：限流键为单一 IP 字符串，IPv6 攻者在 /64 内无限换地址，5/min 建房限流形同虚设；traefik `legengen-rooms` 路由无 rateLimit 中间件兜底。被绕过后：provision 信号量仅 2 并发（洪泛即正常用户全 503）+ **DB 无界膨胀**（`BeginOperation` 与 `CreateRoom` 在业务成功前插入；closed/error/operations 行无任何 TTL/GC，匿名端点可持续注入） | IPv6 /64、IPv4 /24 前缀聚合限流或 traefik 前置 rateLimit；closed 房间保留 N 天后删除；operations 按 created_at 归档 |
| M-S3 | `deploy/prep-room-api.sh:54-57` vs `deploy/traefik-dynamic.yml:12` | **relay 两级开关部署模板自相矛盾**：数据面 `/relay` 路由保留但 UI 层默认 `false`——恰是自家 README.md:94-98 标注"⚠️ 勿用"的组合（中继实际可达但 UI 报"无法直连"，误导且排障困难） | 对齐：摘 `/relay` 路由或 prep 改 `true` |
| M-S4 | `Dockerfile:24-25` + `deploy/prep-room-api.sh:16` + `compose:41-42` | **部署链缺 /data 卷属主**：容器 uid 10001，宿主目录 root:root 755 无 chown → SQLite 写失败 → `log.Fatal` → 全新部署必然 crash loop；照文档复现必失败，且引导运维走向 `chmod 777` 危险解法 | prep 脚本补 `chown -R 10001:10001` |

### 4.2 会话层与 webui 交界（7 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-C1 | `session/peers.go:91-99` vs `roomapi/client.go:429-433` | **PeerRefresher 429 退避只有 15s 下限、没有 60s 上限**：`rateLimitRetryCap=60s` 钳制只实现于 `Client.do()` 重试分支；PeerRefresher 直接拿 `httpErr.RetryAfter`（服务端最大允许 3600s）设 rateLimitUntil。异常/被入侵服务端返回一次 `Retry-After: 3600` → 成员列表轮询停摆 1 小时 | backoff 加 60s 上限与红线对齐 |
| M-C2 | `session/repair.go:104-105` vs `service.go:653-658,1031-1061` | **身份自愈 re-Join 失败时事务补偿清除本地房间记录**：repair 注释承诺"修复过程不改变本地房间记录"，但复用的 `performEnrollment` 是 Create/Join 全量事务——`codeWriteAttempted/metadataWriteAttempted` 置真后任一步失败，defer 补偿 `metadata.Clear()+codes.Clear()`。re-Join 撞网络故障/429 时**用户房间记录被清除**（UI 卡死在 ErrNoRoomMetadata 无更新分支；房主心跳停 → 服务端 5 分钟回收）——自愈机制反成丢房间的诱因 | repair 路径传模式标记，补偿时跳过本地 codes/metadata 清理（同房间保留即正确） |
| M-C3 | `session/service.go:299-307` | **View 的 room_closed 清理路径 forceClear 无超时预算**：`context.WithoutCancel(ctx)` 后仍无 deadline，`Deregister/RemoveProfile` 是无 per-call 超时的 gRPC 调用；对比命令路径有 30s commandTimeout。daemon 进程活着但 gRPC 不响应时，轮询 goroutine 无限阻塞、busy 永久为 true——此后所有命令 `ErrCommandInProgress` 直至重启，且 View 自身走 viewWhileBusy 返回轻量快照，UI 无异常显示，难以诊断 | `WithTimeout(WithoutCancel(ctx), cleanupTimeout)` |
| M-C4 | `webui/express.go:152-153,383-388,500-501` | **give-up 后无任何恢复途径但文案引导"重试"**：注释声称"超过后仅当用户执行新操作时重试"，实现是 `failures>=3` 后永远返回同一错误（failures 仅在 daemonHealthy 快路径或 setup 成功时清零，MSI 持续装不上则永不发生）；文案"请检查安全软件后重试"引导用户做**永远无效**的操作 | 用户显式点击 ExpressPrepareDaemon 时清零 failures；或文案改"请重启应用后重试" |
| M-C5 | `webui/app.go:669-676` + `config/app_config.go:39` | **PerformUpdate 信任前端回传的 downloadURL 与 sha256sum 且验签公钥为空**：URL/hash 由 CheckUpdate 返回 JS 后再回传，Go 侧不校验与 manifest 的绑定；"强制 SHA256"的完整性闭环不成立——任何能驱动前端调用的代码可令管理员进程下载执行任意 exe（WebView 调试/前端供应链投毒） | Go 侧缓存 manifest 绑定，PerformUpdate 无参或仅接受版本号；尽快启用 ed25519 验签 |
| M-C6 | `webui/express_windows.go:106-113,281-300` + `nbdaemon/service.go:113-116` | **修复成功但 gRPC 短暂不可用超过重试预算（15 次/约 90s）时控制器进入"死状态"**：拨号失败分支 inspector probe=nil，`Inspect` 对"服务 Running+probe==nil"恒返回 Unhealthy → `daemonHealthy` 永假 → 用户在冷却后重试触发**多余的 UAC + REINSTALL**（明明组件已就绪） | performDaemonSetup 等待循环中检测 `Installed && Running` 时主动触发重组装或文案指向"组件已就绪" |
| M-C7 | `webui/express_windows.go:146-162,165-181` + `app.go:845-851` | **`reassembleExpress` 失败后控制器被清空但状态快照残留旧房间字段**（closePreviousRPC 先行、assemble 失败仅 Warnf 返回）→ 前端 `isExpressInRoom` 仍真显示假房间视图；检查 BusyCommand 与重组装之间存在 TOCTOU（3s 轮询窗口内用户可发起命令，closePreviousRPC 销毁命令正在使用的 rooms 实例） | 重组装失败时状态回置 NoRoom+clearActiveRoom；重组装与命令互斥 |

### 4.3 经典模式与配置（3 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-W1 | `webui/app.go:411-529` | **经典模式无命令互斥**：`Connect` 全程不持锁编排（`EnsureSoGameAdapter`、`edge.Start` 均在 `a.mu` 外），Wails 每个 JS 调用独立 goroutine → 连点两次"连接"→ before/after 快照差分互相污染（`FindNewWindowsAdapter` 因双实例不唯一报错）、双 edge 进程可通过重入检查（edge.go:209）。极速模式有 beginCommand 互斥，红线未覆盖 classic | App 层引入经典模式 BusyCommand 互斥 |
| M-W2 | `n2n/edge.go:328-338,369,408,440` | **stderrBuf（strings.Builder）跨 goroutine 无同步**：stderr 采集 goroutine 写、Wait goroutine 与"立即退出"路径并发读——race detector 必报；`cmd.Wait()` 返回不保证 stderr 读完，极端下 Builder 并发增长可致 panic 或诊断信息错乱 | 带互斥的缓冲或 channel 收集，Wait 后 join |
| M-W3 | `config/config.go:136-142,294-297` | **Save 的 Remove+Rename 两步窗口崩溃后，LoadOrCreate 的"文件不存在"分支不尝试 `.bak` 直接 `DefaultConfig()`（随机 community）覆盖**——注释宣称"`.bak` 已先行兜底"仅覆盖解析失败/校验失败两条路径；且 config 包整体无并发互斥，多入口并发 Save 在两步窗口交错可丢配置 | `IsNotExist` 分支先 `RestoreFromBackup()`；Save 改 `os.Replace` 语义 |

### 4.4 前端与发布链（6 项）

| # | 文件:行号 | 问题 | 修复要点 |
|---|---|---|---|
| M-P1 | `publish-update.ps1:118-121` + `build-installer.ps1:61-73` + `build-all.ps1:144-147` | **发布链 MSI 哈希校验三处缺口**：publish 的 `-LocalMsiPath` 只查存在不校验 SHA256（build-all 同参数强制校验）；build-installer 预检不复验；build-all 对 `build\bin` 已有 MSI 副本跳过且不校验。坏 MSI 入包 → postinstall 与运行时共用四重校验全失败 → 静默死亡 | 三处全部"校验哈希，不匹配即删+硬失败" |
| M-P2 | `publish-update.ps1:117,127` + `sogame.iss:27` | **NetBird MSI 文件名硬编码三源漂移**（另两脚本从 netbird-release.json 动态读）→ 版本升级改了 release.json 忘改这两处即静默错拿；`:127` 失败提示参数名写错（`-MsiPath` 实为 `-LocalMsiPath`） | publish 改读 release.json；iss 用 `/DNetBirdMSI=` 注入 |
| M-P3 | `sogame.iss:61,110-116` | **postinstall 失败完全静默**：不检查退出码不读 result 文件，helper 失败安装照样"成功完成"；`PrivilegesRequiredOverridesAllowed=dialog` 允许的非管理员安装场景下"安装器已提权不会再弹 UAC"的注释不成立（helper asInvoker + msiexec 装系统服务必失败） | 读 result 失败时提示"网络组件未预装完成，首次使用时自动补齐"；删 dialog override 或该场景跳过 |
| M-P4 | `frontend/src/App.jsx:133,239-250,660` | **经典模式"测速"按钮自启动后永久卡死**：`setLoadingAction('latency')` 后成功/失败路径均无复位（事件回调只复位独立的 `latencyLoading`）→ 挂载时即调用 → 按钮恒"测速中..."且 disabled，仅当用户执行了其他操作（各有 finally 复位）才意外解卡 | try/finally 复位或改用独立 state |
| M-P5 | `webui/app.go:861-868` + `roomapi/client.go:643-659` + `webui/express.go:791` | **用户向缺陷两处**：① `ExpressGetState` 兜底 Action 仍是"请检查守护进程"——守护进程文案红线唯一漏改点（同文件其余 6 处兜底合规，且 express_daemon_test.go 有机制化校验——恰是此分支在测试外）；② 房间码格式错误（裸 error 无哨兵）落入兜底分支"操作未完成/稍后重试"——**误导性重试引导**（重试无效），前端输入框仅 maxLength 无 `XXXX-XXXX-XXXX` 预校验 | 兜底文案改合规；格式错误加专用分支；前端加正则预校验+自动大写 |
| M-P6 | `main.go:116-120` | **`--update-apply` 子进程以提权 O_TRUNC 写固定路径日志**：`%TEMP%\sogame-update-apply.log` 无 reparse 检查——同用户 medium 进程预置 symlink/junction 指向任意文件 → 管理员子进程截断覆盖目标（与 H-5 同族的固定路径提权写点，内容为更新日志半受控） | 写前 Lstat 拒绝 reparse；或改随机后缀文件名 |

---

## 5. 低危问题（30+，按模块归类，均为真实代码依据的精选）

**Room API 服务端（7 项）**：`MarkRoomClosed` 失败后房间永久卡 closing，房主重试 Close 得"假 204"（`BeginClosing` CAS 失败分支 `return nil`，service.go:704-706）而唯一恢复路径是重启；`OwnerOfflineAfter<=0` 部署下 RequeueClosingRooms 拨回的房间无后续回收者，NetBird 资源（有效 SetupKey+Policy）永久存活；`listPeers` 缓存击穿无 singleflight（service.go:520-537，检查与写入间锁已释放，64MB 响应上限 × N 并发）；房间码取模偏置 `256%31=8` 前 8 字符概率高约 3%（service.go:854-863，60bit→59.4bit，标准做法是 rejection sampling）；`operations`/终态房间行正常运营也无界累积（healthcheck 不可见）；traefik 一旦开启 accessLog 明文房间码将绕过服务端全部脱敏防线（部署文档无红线提醒）；`NETBIRD_MANAGEMENT_URL` 无 scheme 强制（误配 `http://` 时全账号 PAT 明文传输）+ 弱口令密钥无盐 SHA256 派生无 KDF 强化。

**会话层与 roomapi 客户端（9 项）**：View 410 的 beginCommand 失败分支返回 ErrRoomClosed 与"busy 时本轮跳过"注释矛盾，窄窗口误清新命令 UI 状态（service.go:296-301）；心跳 goroutine 对令牌/房间码 Load 失败静默 return 无日志无重试——房主心跳无痕静停，服务端 5 分钟后回收且日志无线索（service.go:932-940）；OwnerTokenStore 初始化失败仅 WARN 降级，创建的房间 5 分钟后必被看门狗静默回收、用户无感知（express_windows.go:128-136）；Redact 不含 IP 规则，与 AGENTS.md"脱敏（…IP…）"表述偏差（View 轮询每 5s 明文记本机虚拟 IP，service.go:332）；给 repair 加 recoverable 状态但 viewWhileBusy 快照丢失 Disconnected/IsOwner/ResumePending 派生字段导致命令期间 UI 闪烁（service.go:374-394）；PeerRefresher 无单飞，5s 轮询与命令后刷新可并发双请求白耗限流配额（peers.go:72-81）；EnsureDaemonReady 快路径单次 Inspect 失败（3s 超时，服务刷新瞬间即可发生）即触发完整 setup+UAC——与自愈路径"4 次观测防抖"的稳健度不对称（express.go:361-366）；死代码群：`session.Switch`（仅测试引用+错误映射残留）、`RecoveryMonitor.Watch`+`RPCAdapter.Subscribe` 本体（使用点已删本体残留）、`netbird.RepairResultFor/RepairResult/RepairReason`（EnsureDaemonReady 不再消费）、`ExpressService.RepairRequired` 字段（旧"修复"概念 UI 残留，前端只消费 daemonNeedsSetup）——旧概念残留有违"守护进程一体化"红线的回归风险。

**经典模式与基础设施（14 项）**：tap-adapter.json 非原子写且损坏后不自愈（store.go:96，每次启动重复告警）；邀请码 v2 编解码对含 `|` 的 community 不自洽（`ValidateCommunity` 允许 `|` 但 v2 解码 `SplitN` 字段错位——自己生成的邀请码自己解不开，app.go:253-318）；`NATTypePortRestricted` 是永不返回的死枚举（两步探测算法上无法区分 Full Cone 与 Port Restricted，用户被误判为 FullCone 收到"直连成功率极高"误导建议——natdetect 零测试正是其潜伏原因）；edge 行匹配状态机对畸形输出宽泛（"peer"+"added" 两词同现即 registeredPeers++ 虚增；StateRegistered 后任何含 "disconnected" 的行打回状态且无守卫，edge.go:874-935）；stdout/stderr Scanner 超 64KB 行静默退出且 `scanner.Err()` 从不检查，goroutine 退出后 edge.exe 写满缓冲**挂起**（edge.go:318-338）；checkVirtualAdapter 三分支恒返回 true 形同虚设（dependencies.go:113-138）；OpenLogs 启动 notepad 后从不 Wait 泄漏句柄（app.go:560-562）；logger Close 之后所有日志静默丢弃无 stderr fallback（logger.go:157-166）；`IsLikeDescription` 导出死代码；generateStableIP 只用 1 字节熵（hash[:4] 解出 2 字节只用 b[0]，254 槽位碰撞 → edge 注册冲突而重试仅 2 次）+ MeasureAllNodesLatency 无 ctx 串行最长约 42s；`cfg.MgmtPort = mgmtPort` 突变调用方传入的 `*config.Config`（当前调用方值拷贝无恙但 API 契约脆弱）；firewall.go 的 Environ append 不清同名旧值（重复键行为未定）；STUN 的 `ResolveUDPAddr/DialUDP` DNS 阶段不受 ctx 管辖（红线 5 在 SetDeadline 已兑现，DNS 段拖长 10s 上限）；版本资源三源漂移 `build/windows/info.json` "2.0.0.0" ≠ wails.json "2.1" ≠ AppVersion "2.1"（exe 文件属性显示 2.0.0.0 而界面显示 2.1）。

**提权与更新（8 项）**：签名 payload 不含 DownloadURL/Size/MinVersion（篡改 URL 不破坏签名——当前被 zip 哈希钉死无实质增益，但构成管理员进程任意 URL GET 面且未来语义改动立即破坏信任链）；`Check` 不约束 URL scheme（http 降级无校验）；CRL 不可达降级 `WTD_REVOCATION_CHECK_NONE` + 指纹钉扎组合——证书私钥泄露被 CA 吊销后离线环境继续信任（被内嵌 SHA256 封死，信息性）；`compareVersion` 用 `fmt.Sscanf` 静默吞错（"2.1-beta"按 [2,1,0] 比较，验签后 version 在签名内，防御纵深缺陷）；`hide_windows` 清理作用于运行 helper 的管理员账户，UAC 跨账户场景（管理员 B 提权、登录用户 A）下 A 的 Run 残留/桌面快捷方式不清（AUTOSTART=0 为主防，兜底失效）；EnsureDaemonReady 失败计数在快路径短暂 ready 即清零——3 次上限只防"连续"失败，与"3 次停手"对外承诺语义有偏差；`securestore` metadata 明文 JSON 向同用户进程暴露 RoomID/ManagementURL/ProfileID（与 AGENTS.md 声明一致的设计取舍，不含入会凭证，如实记录）；`updater` 的 `maxDownloadBytes` 流式上限与 ContentLength 不一致分支无测试。

**前端与部署（5 项）**：`frontend/dist` 半入库（仅 index.html 被跟踪且引用不入库的 assets hash——跳过 npm build 直接 `go build .` 会嵌入引用不存在资产的入口，运行白屏）；App.jsx 结构债（978 行单文件、双通道状态同步无版本号防乱序、reveal effect 离开房间瞬间在途 promise 竞态、`expressNoticeTimerRef` timeout 无 cleanup、`repair-btn` 类名残留、handleExpress* 高度重复）；`ExpressRevealRoomCode` 的 d.ts 声称 `Promise<string|app.ExpressError>` 与运行时 reject `e.Code` 语义三重漂移（B-1 次生）；virdy.cn 更新入口 80 明文直服务无 HTTPS 跳转（签名未启用的当前兼容模式下的残余暴露面，建议加 redirectScheme）；设计文档参数漂移（DAEMON_AUTO_SETUP_DESIGN.md:175 写 `--msi` 实际 helper 参数为 `--artifact`；:119 "等待 30s" 未实现；:144 "90s" 失效——按文档维护会传错参数）。

---

## 6. 上一轮（2026-11-12）修复验证结论——专项复核

对上轮报告 §10 修复状态总表逐项抽查（重点：声称已修复的安全机制是否真实落地、边缘 case 是否闭环）：

**全部在位且质量良好**：B-1 RepairService 死锁（先判条件再持锁推进，:62-65/:116-119）；H-9 Service.Close；H-10 View room_closed 命令互斥+RoomID 二次校验；H-4 XFF 最右值（含 3 个测试）；H-5/H-6/H-7 服务端补偿三连；H-8 Default policy 自检；M-S6 WithoutCancel；M-S7 RequeueClosingRooms；M-S10 HMAC+域分离派生；M-W5 日志脱敏三缺口（fallback 全级别过 Redact、BuildArgsForLogging 同源脱敏、Redact 补 `-k`/32hex 规则+LogValuer 旁路）；M-U3 AES-GCM v2+CFB 兼容；M-U4 无 BOM 写入+首字节自检；房间码红线（快照恒空串+Reveal 单通道+测试机制化）；updater ZipSlip/保留名/ADS/CreateTemp；`--update-apply` targetDir 校验+writeFileAtomic。**服务端八条自称契约红线经逐条验证与实现零漂移**（SQL 参数化/常数时间比较/HMAC 迁移/审计路由模板/Retry-After/Reconcile 四阶段/WithoutCancel/404 容忍）。

**存在残留或边缘缺口的 4 项**：
1. **H-1/H-2 helper 路径防护**：最终文件级 reparse 拒绝+原子 rename+白名单均落地，但**中间目录链未校验**——本轮 H-5（跨用户 junction 预置攻击面仍开）；
2. **M-W1 断开取消挂起重启**：manualStop+pending 双标志在位，但冷却回调只查 `manualStop` 不查进程代际——本轮 H-10（旧配置劫持）；
3. **a.cfg 快照化**：Connect 等多数入口值拷贝在位，`ExpressCreateRoom/ExpressJoinRoom` 两处漏网（指针别名）——本轮归并 M-C7 组；
4. **"429 档 60s 钳制"红线**：`Client.do()` 已实现，**PeerRefresher 路径无上限**——本轮 M-C1（红线声称与实现不完全相符）。

**上轮遗留未修项复核**：H-11 edge argv 明文 key（接受现状）、H-14 主程序 requireAdministrator（接受现状，本轮 H-7 是其注入面的具体化）、rooms 测试无 CGO 执行环境（**至今仍是最 大流程风险**，上轮新增的 H-5/H-6/M-S7 回归用例在两轮审计中均未真实运行过）均维持原状。

---

## 7. 测试覆盖评估（含本轮实际运行结果）

**本轮实际运行**：主 module `go vet` 除 `nic/query.go:80`（unsafe.Pointer 已知误报，GetIfTable2Ex 场景，源码已注明）外干净；`go test -count=1 ./internal/... ./cmd/... .` **22 个有测试的包全部通过**（config/logger/n2n/nbdaemon/netbird/nic/observability/poll/roomapi/securestore/security/session/tap/updater/webui/main）；room-api `go vet` 干净、`httpapi` 测试通过，`rooms/store` 受 CGO_ENABLED=0 环境限制**仍无法执行**。

| 区域 | 覆盖状况 | 关键缺口 |
|---|---|---|
| `internal/session` + `roomapi` | 好（64+26 用例：状态机全态/补偿链/心跳即时性/自愈防抖/幂等/防泄露） | repair 失败对本地记录的影响（M-C2）；心跳 Load 失败分支；View busy 竞态下 410 分支 |
| `internal/webui` | 中（daemon-setup 状态机 5 用例质量好） | **Wails 桥接语义零测试——B-1 必现 bug 的直接根因**；`reassembleExpress`/`closePreviousRPC` 与命令并发的竞态（H-2/H-4/M-C7 全在此交界处）零覆盖；建议加"反射断言所有绑定方法第二返回值为 error 接口"契约测试 |
| `server/room-api` | rooms 12 用例+httpapi 覆盖好 | **HMAC 在线迁移零回归测试**（上轮修复项最大测试缺口）；M-S1 心跳续命 vs 旧快照竞态；限流器（容量挤出/Retry-After/窗口边界）整体零测试——主要安全控制无覆盖；`internal/config`/`crypto`/`audit`/`netbird`（服务端侧）零测试 |
| `internal/n2n` | 纯函数已测（BuildArgs 脱敏/注册判定） | **并发行为零测试**（Stop/Start 交替、自动重启冷却、registrationRetry 状态机）——H-9/H-10 无法被现有测试捕获 |
| `internal/config` | 迁移端到端覆盖好 | **失败路径零测试**（损坏 YAML、解密失败、.bak 恢复、Remove/Rename 崩溃窗口）——H-8/M-W3 恰潜伏于此 |
| **`cmd/sogame-helper`** | **零测试** | **整个提权辅助程序无任何测试**：validateWritablePath/rejectReparsePoint/writableRoots 这组 H-1/H-2 修复的核心防护零单测（8.3/大小写/尾部点/UNC/junction 边缘全靠人工回归）；`prepareElevation`/`verifyHelperDigest`（UAC 前完整性校验链）零测试 |
| `internal/platform`/`natdetect`/`diagnostics`/`releasebuild`/`cmd/nicctl` | **零测试** | STUN 解析纯函数极适合单测（死枚举正因无测试存活）；runPowerShell 的 env 传参契约可注入假路径测试 |
| **`frontend/`（978 行 App.jsx）** | **零测试** | 无框架无脚本；M-P4 类问题无回归保障；npm 无 test/lint/audit 门禁 |

**流程风险**：仓库无 CI 配置可查证；room-api 关键回归与 `-race` 在本机均不可执行，并发正确性结论（含本轮 11 条高危中 6 条并发相关项）完全依赖静态分析；建议优先落地"有 gcc 的机器或 CI 每版本发布前强制执行 `CGO_ENABLED=1 go test ./... -race`"。

---

## 8. 文档与实现不一致清单（本轮增量）

| # | 文档描述 | 实际情况 |
|---|---|---|
| D-1 | DAEMON_AUTO_SETUP_DESIGN.md:144 "daemon-setup 单独用 90s 预算" | UAC/msiexec 阶段 ctx 不贯通，实际最长 10 分钟（H-1） |
| D-2 | DAEMON_AUTO_SETUP_DESIGN.md:119 "已有房间命令在跑则等待其完成（最多 30s）" | 未实现：无条件覆盖 BusyCommand（H-4） |
| D-3 | DAEMON_AUTO_SETUP_DESIGN.md:152,175 `--msi` 参数"已具备" | 实际参数为 `--artifact`（按文档维护会传错） |
| D-4 | express.go:152 "超过重试上限后仅当用户执行新操作时重试" + 文案"检查安全软件后重试" | give-up 后新操作也永远拒绝（M-C4） |
| D-5 | AGENTS.md §7 "Retry-After 钳制（429 档 60s）" | 仅 Client.do() 重试分支实现；PeerRefresher 路径无上限（M-C1） |
| D-6 | AGENTS.md §7 "日志脱敏（…IP…）" | Redact 不含 IP 规则，仅诊断导出路径的 Anonymize 覆盖 |
| D-7 | AGENTS.md §7 "调用 msiexec/powershell 等系统程序一律 System32 绝对路径" | msiexec/powershell 合规；ping/tasklist/taskkill/netsh/pnputil/ipconfig/notepad 共 12 处经 PATH 解析（H-7） |
| D-8 | publish-update.ps1:86-88 "两脚本 helper 构建 flags 必须一致" | 三脚本互不相同（H-11） |
| D-9 | config.go:273-275 "Save 崩溃由 .bak 兜底" | Remove/Rename 两步窗口 + IsNotExist 分支不读 .bak（M-W3） |
| D-10 | `ExpressRevealRoomCode` d.ts `Promise<string|app.ExpressError>` | 运行时成功 resolve(null)、失败 reject Code 字符串（B-1 次生） |
| D-11 | `frontend/dist` 入库意图 | 仅 index.html 半入库，引用不存在资产（构建白屏陷阱） |

---

## 9. 修复优先级路线图

**P0（发版阻断，立即）**
1. **B-1**：`ExpressRevealRoomCode` 改 `(string, error)` + `wails generate module` + 桥接契约反射测试——当前所有发布版邀请功能断裂。
2. **H-11**：统一三脚本 helper 编译 flags——任何人走 build-installer 补编译路径即产出自校验必失败的安装包。
3. **H-5**：helper 写路径中间目录 reparse 链校验——上轮修复的跨用户攻击面残余。

**P1（本周）**
4. **H-1 + H-4**：ctx 贯通 `launchElevation`（90s 预算兑现）+ `performDaemonSetup` 占位前检查 BusyCommand——守护进程一体化互斥收口；Leave/Disconnect 对 daemon-setup 放行。
5. **H-2 + M-C6**：`waitForDaemonAndReassemble` 重组装前检查 busy；probe=nil 死状态主动重组装。
6. **H-3**：重组装继承已连接状态/自动恢复心跳——消除"改设置拆房间"。
7. **H-8**：config 解密失败置空 Key——消除"重装系统即丢房间身份"。
8. **H-6**：热更新 staging 目录 ACL 收紧或复制前整体重验。
9. **H-9 + H-10**：edge Wait goroutine 用局部 cmd + 重启冷却代际检查。
10. **H-7**：12 处系统程序 System32 绝对路径化 + iss `{sys}\netsh.exe`。
11. **M-S1**：看门狗 CAS 合并过期复查。

**P2（下个迭代）**
12. **M-C2**：repair 路径补偿不清本地记录（自愈丢房间）；**M-C3**：View forceClear 加超时预算；**M-C4**：give-up 恢复途径；**M-C1**：429 退避上限。
13. **M-W1/M-W2/M-W3**：经典模式互斥 + stderrBuf 同步 + config 恢复链。
14. **M-S2**：IPv6 前缀聚合限流 + closed/operations TTL；**M-S4/M-S3**：部署一致性两处。
15. **M-P1/M-P2/M-P3**：发布链 MSI 哈希三缺口 + 文件名三源 + postinstall 失败反馈。
16. **M-P4/M-P5/M-P6/M-C5/M-C7**：前端测速卡死 + 文案红线漏改 + 固定路径日志 + PerformUpdate 绑定 + 重组装状态残留。
17. **测试补强**（防 B-1/H-2/H-9 类复发）：桥接契约反射测试、sogame-helper 单测（白名单/reparse 边缘）、n2n 并发测试、config 失败路径、HMAC 迁移回归、webui 重组装并发、CI 落地 CGO+race。

**P3（持续）**
18. 低危批量清理（死代码群——`Switch`/`Watch`/`RepairResultFor`/`RepairRequired` 旧概念残留，natdetect 死枚举与测试，logger Close fallback，dist 入库策略等）。
19. 文档对齐（D-1~D-11）；前端组件拆分与最小测试集；主程序降权架构评估（与上轮 H-14 合并跟踪）。

---

## 10. 审计方法与底稿索引

本报告由 6 路审计汇总去重而成：5 份模块级审计底稿（并行静态审计）+ 审计者对阻塞与全部高危发现的逐条人工复核（源码 + Wails v2.12.0 运行库源码 + 测试实际运行）。行号以审计基线 `ba50d93` 为准。

1. 客户端极速模式核心（`internal/session`、`internal/webui/express*`、`internal/roomapi`、`internal/netbird`）：阻塞 1 / 高 5 / 中 8 / 低 9
2. 提权与安全面（`internal/nbdaemon`、`cmd/sogame-helper`、`internal/securestore`、`internal/updater`、`internal/releasebuild`、`main.go`）：高 3 / 中 2 / 低 8
3. Room API 服务端（`server/room-api` 全部 + `deploy/` + `tools/`）：中 4 / 低 7
4. 经典模式与基础设施（`internal/n2n`、`tap`、`nic`、`platform`、`config`、`observability`、`logger`、`security`、`poll`、`natdetect`、`diagnostics`、`cmd/nicctl`、`webui/app.go`）：高 3 / 中 3 / 低 14
5. 前端与构建分发链（`frontend/`、`installer/`、`scripts/`、`tools/`、`wails.json`、`build/`）：阻塞 1（与底稿 1 去重）/ 高 2 / 中 6 / 低 5

**去重说明**：90s 预算失效（底稿 1/2/5 三路独立发现）、performDaemonSetup busy 覆盖（底稿 1/2）、构建 flags 漂移（底稿 2/5）、cfgSnapshot 指针别名（底稿 4/5）、新装系统重置配置（底稿 4 独立发现 H-8 与底稿 5 联合确认）已合并计数。

**残余风险声明**：并发类结论（含 6 条高危）基于静态分析，本机无 `-race` 执行环境；`rooms` 测试两轮审计均未真实运行；服务端部署侧发现（M-S2~M-S4）依赖生产环境实际形态验证；frontend 行为类发现（B-1 前端表现、M-P4）基于代码推演未做运行时复现（B-1 的桥接机制已经 Wails 源码逐级验证）。

---

# 第二部分：补充验证（2026-11-24 12:00，权限放开后执行）

> 本部分补齐第一部分 §10 残余风险声明中列出的全部未完成验证：① rooms 测试真实运行、② `-race` 竞态检测、③ B-1 运行时复现。
> **验证环境**：MinGW-w64 GCC 16.2.0（POSIX threads, UCRT；winlibs 发行版，经 winget 下载通道获取 zip 后以 tar 解压至临时目录），`CGO_ENABLED=1`——两轮审计以来首次在本机打通 room-api 的 CGO 构建链。

## 11. rooms 测试首次真实运行——上轮遗留回归用例全部通过

`server/room-api` 全部测试（`CGO_ENABLED=1 go test -count=1 ./...`）：`httpapi` 0.216s、**`rooms` 1.238s 通过**（历史首次真实运行）。

关键确认——2026-11-12 修复时新增、两轮审计期间从未执行过的回归用例**全部通过**：

| 上轮修复项 | 对应用例 | 结果 |
|---|---|---|
| H-5 幂等重试 CAS（防双建房） | `TestResetOperationCAS` | ✅ |
| H-6/H-7 补偿统一（复用 group 不误删 + Revoke+Delete 双管） | `TestCompensateProvisioningNeverDeletesReusedGroup` | ✅ |
| M-S7 closing 僵尸重排队 | `TestRequeueClosingRooms` | ✅ |
| teardown 失败回滚保持 active 可重试（真实断言版） | `TestCloseTeardownFailureKeepsActiveAndRetryable` | ✅ |

上轮报告 §13"最大流程风险"（关键回归用例从未真实运行）**就此解除**：上轮修复的服务端并发回归保障经真实执行验证成立。本报告 §7 测试覆盖评估中"rooms 受 CGO 限制无法执行"的表述以本节为准更新。

## 12. `-race` 竞态检测——21/22 包干净；新发现 1 项测试自身竞争

**room-api**（`CGO_ENABLED=1 go test -race -count=1 ./...`）：`httpapi` 1.201s、`rooms` 2.339s——**race 下全部通过**。

**主 module**（22 个有测试的包）：21 包 race 通过；1 包失败——

### 【新发现 · 低】V-1 `robustness_test.go:73-99` 测试自身数据竞争，`go test -race` 必然失败（3/3 稳定复现）

```go
// robustness_test.go:73-77,99
requests := 0
server := httptest.NewServer(http.HandlerFunc(func(...) {
    requests++                    // :75  handler goroutine 写
    time.Sleep(6 * time.Second)
}))
...
if requests != 1 {               // :99  测试主 goroutine 读——无同步原语
```

`requests` 计数器跨 `httptest` handler goroutine 与测试主 goroutine 无同步（客户端超时放弃后 handler 仍在 sleep，读取与写入之间无 happens-before 边）。**生产代码无此问题**（`client.go` 的 per-attempt 超时语义经该测试验证正确：elapsed≈2s、无重试、requests==1）；属测试可靠性缺陷。
**修复**：`requests` 改 `atomic.Int32`（`requests.Add(1)` / `requests.Load()`）。

**重要佐证**：该竞争在 `-race` 下确定性失败——**该仓库的 `go test -race` 在本机首次运行即被抓出**，反证仓库历史上线（含 CI 若存在）从未跑过 race 检测，与第一部分 §7"无 CI 配置可查证"的判断闭环。

### 静态并发结论的验证边界（如实界定）

race 通过**不构成**对 H-2/H-4/H-9/M-W2 等静态发现的证伪——race detector 只能捕获实际发生的数据竞争，而这些发现需要的触发时序恰是现有测试的盲区：

| 静态发现 | 现有测试是否覆盖触发路径 | race 结果与结论 |
|---|---|---|
| H-9/M-W2（edge `e.cmd` 字段竞态、stderrBuf Builder 竞争） | 否——n2n 现有测试仅覆盖纯函数（BuildArgs/注册判定），不启动真实进程 | 维持静态结论：M-W2 属 race detector 必报型（strings.Builder 无锁并发读写），一旦有进程编排测试即可机械捕获 |
| H-2/H-4/M-C7（webui 重组装/BusyCommand 覆盖竞态） | 否——webui 测试集中于 daemon-setup 状态机，`reassembleExpress`/`closePreviousRPC` 并发交错零覆盖 | 维持静态结论；`internal/webui` race 包级通过仅证明已测路径干净 |
| session 层（M-C2 修复事务、心跳 Load 失败等） | 否——对应失败分支无测试 | 维持静态结论 |
| 服务端 M-S1（看门狗旧快照竞态） | 否——`TestTeardownDoesNotResurrectClosedRoom` 只覆盖"已 closed"场景 | 维持静态结论；`rooms` race 通过仅证明已测路径干净 |

由此强化第一部分 P2 测试补强的两条具体建议：① n2n 进程编排测试（可用假可执行文件 + 可控输出驱动 Stop/Start 交替与自动重启冷却）落地后，M-W2/H-9/H-10 可被 race 检测机械验证；② CI 必须先修 V-1 才能开启 race 门禁（当前 `-race` 全量必红）。

## 13. B-1 运行时实证——从"源码级推演"升级为"运行时复现"

以 reflect 精确复刻 Wails v2.12.0 桥接链（`boundMethod.go:101-106` → `dispatcher/calls.go:48-60` → `runtime/desktop/calls.js:180`），对同构签名 `(string, *ExpressError)` 执行成功/失败双路径，运行输出：

```text
=== 成功路径（RevealRoomCode 成功返回房间码）===
[boundMethod] returnValue="ABCD-EFGH-JKLM"  err==nil: false  (typed-nil 装箱后断言成功且非 nil)
[dispatcher] json={"result":null,"error":""}  (房间码 "ABCD-EFGH-JKLM" 被丢弃)
[calls.js ] message.error="" -> falsy=true => resolve(message.result=<nil>)

=== 失败路径对照（expressPublicError）===
[dispatcher] json={"result":null,"error":"NETBIRD_SERVICE_UNAVAILABLE"} => JS: reject("NETBIRD_SERVICE_UNAVAILABLE")
```

三点结论以运行时证据钉死：
1. **成功路径**：Go 侧明明返回房间码，桥接后 JSON 为 `{"result":null,"error":""}`，JS 侧因空串 falsy 走 `resolve(null)`——不是 reject，前端 `.catch` 不触发、`.then(code => { if (code) ... })` 对 null 静默跳过，**完全无声**。B-1 的"发布版房间码永不显示"成立。
2. **失败路径**：前端 `reject("NETBIRD_SERVICE_UNAVAILABLE")`——拿到的是**英文错误码**而非中文 Message，错误展示链路同样损坏（`setErrorMsg(String(e))` 会把英文码直接上屏）。
3. typed-nil 陷阱是语言层机制（与具体业务类型无关），第一部分 B-1 修复建议中的"反射契约测试"可机械性拦截同类问题。

## 14. 第一部分残余风险声明更新（逐条闭环）

| 第一部分 §10 声明 | 本部分结果 |
|---|---|
| "并发类结论基于静态分析，本机无 `-race` 执行环境" | ✅ 已运行（主 module + room-api 全量）：已测路径干净；静态并发发现维持"现有测试未覆盖触发时序"的定位，验证边界见 §12 |
| "`rooms` 测试两轮审计均未真实运行" | ✅ 已运行且全部通过，上轮 4 个回归用例验证成立（§11） |
| "frontend 行为类发现（B-1）基于代码推演未做运行时复现" | ✅ B-1 三级桥接链运行时复现闭环（§13）；M-P4 前端运行时复现仍需 UI 环境，维持静态结论（其证据链纯 JS 语义，风险低） |
| "服务端部署侧发现（M-S2~M-S4）依赖生产环境实际形态验证" | ⚠️ 本机无法验证生产拓扑（IPv6 暴露、chown 步骤、relay 路由形态），维持原结论——建议在服务器上按 M-S4 复现一次全新部署冒烟 |

**发现增量**：+1 低（V-1 测试自身数据竞争，§12）；上轮修复的服务端回归用例经真实执行全部验证通过，无修复失效项。
**环境持久化建议**：本机 MinGW-w64 GCC 16.2.0 位于 `C:\Users\Administrator\AppData\Local\Temp\zhanlu\mingw64`（临时目录，重启清理策略未知）——建议将其迁移到固定路径（如 `C:\mingw64`）并写入 AGENTS.md §5 的 room-api 测试说明，使 `CGO_ENABLED=1` + `-race` 成为每版本发布前的本地可执行步骤（修复 V-1 后即可全量开 race 门禁）。


