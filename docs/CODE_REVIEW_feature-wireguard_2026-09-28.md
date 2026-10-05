# feature/wireguard 分支代码评审报告（2026-09-28）

> 本文档记录对 `feature/wireguard` 分支相对 `main` 全部改动的一次性代码评审结果，
> 供修复跟踪与合并决策使用。评审为只读静态评审 + 关键发现逐行复核，未修改任何代码。

## 0. 评审元信息

| 项 | 内容 |
|---|---|
| 评审范围 | `main...feature/wireguard`（merge-base `37fd16b`），52 个提交，核心代码约 26.4k 行新增 |
| 覆盖模块 | `server/room-api/`（全新服务端）、`internal/session`、`internal/webui/express*`、`internal/roomapi`、`internal/netbird`、`internal/nbdaemon`、`internal/securestore`、`internal/updater`、`internal/releasebuild`、`cmd/sogame-helper`、`internal/config` 迁移链、`internal/natdetect`、`main.go`、前端契约、打包/部署脚本 |
| 评审日期 | 2026-09-28 |
| 评审方法 | 分模块并行深度评审；全部阻塞/高危项由第二名评审者逐行独立复核确认 |
| 测试基线 | 客户端 `go test ./internal/...` **全部通过**；room-api 因本机网络受限无法下载 `go-sqlite3`（CGO 依赖），`go test ./...` **未能运行**（见 §8 附注） |

**结论：存在 4 个阻塞问题，修复前不建议合并 `main` 或发版。**

---

## 1. 阻塞（合并/发版前必须修复）

### B1. 热更新自应用链路完全失效——`--update-apply` 从安装目录复制到安装目录

- **位置**：`main.go:87` + `internal/webui/app.go:650-651`
- **问题**：`runUpdateApply` 以 `srcDir := filepath.Dir(os.Args[0])` 为源目录；而 `PerformUpdate` 启动子进程的方式是 `exec.Command(exe, "--update-apply", filepath.Dir(exe))`，`exe` 是安装目录中当前运行的 exe，子进程 `os.Args[0]` 即该完整路径。结果 `srcDir == targetDir == 安装目录`：解压到 `%TEMP%\sogame-update` 的新版本文件**从不参与复制**，exe 自身因文件锁写失败被跳过，最后 `cmd.Start()` 启动的还是旧版本。`cmd.Dir = extractDir`（app.go:651）说明原意图是读工作目录，但代码读的是 `os.Args[0]` 的目录。用户视角：更新包下载解压正常，重启后仍是旧版，**静默失败**——整条链路从未被真实验证过。
- **修复**：
  1. `PerformUpdate` 改为执行解压目录里的 exe：`exec.Command(filepath.Join(extractDir, "SoGame.exe"), "--update-apply", filepath.Dir(exe))`，使 `os.Args[0]` 天然指向源目录（推荐）；或 `runUpdateApply` 内用 `os.Getwd()` 取 `extractDir`；
  2. 联动修复 M7（解压目录固定且不清理）；
  3. 修复后手工验证一次完整更新流程，并补集成测试（见 §6）。

### B2. room-api `teardownRoom` 无状态 CAS 且把 NetBird 404 当失败——已关闭房间可被复活为 active

- **位置**：`server/room-api/internal/rooms/service.go:505-508, 558-561` + `server/room-api/internal/netbird/client.go:160-163` + `service.go:399-401`
- **问题**：
  1. `SetStatus('closing')` **无条件执行**；`Close` 只拦截 `closed` 不拦截 `closing`；看门狗快照（service.go:460）与执行之间存在窗口：快照含房间 X → 房主同时 `Close` X 成功（status=`closed`，资源已删）→ 看门狗对 X 执行 `teardownRoom` → `closing` **覆盖掉 closed** → 所有 NetBird 删除返回 404 → `client.do` 把 404 当 error → failures 非空 → `SetStatus('active')`。**已关闭房间被复活为 active**，`Join` 可查到并下发已被吊销的 setup key，看门狗每轮反复失败刷错误日志，形成永久僵尸。
  2. 代码注释（service.go:504）声称"NetBird 侧的删除动作天然幂等"，与 `client.do` 的实际行为（404=error）相反。同理，管理员在 NetBird dashboard 手动删过任一资源后，该房间永远无法关闭。
  3. 两个并发 `Close`（或 `Close` 与看门狗）无互斥，读-改-写非原子。
- **修复**：
  1. store 增加条件更新：`UPDATE rooms SET status='closing' WHERE id=? AND status='active'`，`RowsAffected==0` 时直接返回（已关闭视为幂等成功）；
  2. `netbird.Client` 返回类型化错误（如 `ErrNotFound`），`teardownRoom`/`Reconcile`/`Disable` 对删除类调用的 404 视为成功；
  3. `Close` 入口对 `closing` 状态显式处理（继续 teardown 或返回冲突）。

### B3. room-api 启动 `Reconcile` 与看门狗的 legacy 兼容策略直接矛盾——每次重启/发版批量误杀旧客户端活跃房间

- **位置**：`server/room-api/internal/rooms/service.go:297-317` + `internal/store/store.go:330-344`（`ListStaleActiveRooms`）对比 `store.go:230-233`（`ListOwnerSweepCandidates`）
- **问题**：`ListOwnerSweepCandidates` 明确排除从未心跳的房间，注释写明"旧版客户端建房后没有心跳能力，混部期间不能把用户正常房间误杀"。但 `ListStaleActiveRooms` 的条件是 `status='active' AND (心跳超时 OR (从未心跳 AND created_at < 30分钟前))`，且 `Reconcile` 在**每次进程启动**时执行（cmd/room-api/main.go:77）——即 room-api 每次重启/发版/容器重建，所有旧版客户端创建的、创建超过 30 分钟的正常在用房间会被立刻删 Policy/SetupKey/Group 并 disable。两条路径的设计意图截然相反，必有一个是错的。
- **修复**：统一策略。若采纳看门狗的兼容语义，`ListStaleActiveRooms` 去掉"从未心跳"分支（或加 `owner_token_hash <> ''` 限定）；若采纳 Reconcile 语义，需明确旧客户端淘汰时间表并同步改看门狗注释与 AGENTS.md。

### B4. 房间码明文进日志（客户端 + 服务端双侧）——违反 AGENTS.md §7 安全红线

- **服务端**：`server/room-api/internal/httpapi/server.go:73-79` 审计逐请求记录 `r.URL.Path`，`/rooms/{code}/peers|close|heartbeat|disable` 路径含明文房间码——房间码即入会凭证，任何能读到日志的人（日志收集系统访问面通常大于服务器本身）即可 `Join` 任意活跃房间。
- **客户端**：`internal/roomapi/client.go:442` `logFailure(method, endpoint.Path, fmt.Sprintf("连接失败: %v", err))`，`err` 是 `*url.Error`，其 `Error()` 含完整 URL（如 `Post "https://legengen.top/rooms/ABCD-1234-WXYZ/heartbeat": dial tcp ...`）；`logFailure`（client.go:485-493）只对 `path` 参数过 `observability.Redact`，`detail` 原样拼入；`TransportError.Error()`（client.go:499-503）同样内嵌含 URL 的 cause 文本并向上传播，在 `internal/session/service.go:966` 心跳失败 WARN 落盘（网络故障时每 60~120s 一条）。受影响端点：`postOwnerAction`（`/rooms/{code}/close|/heartbeat`）与 `Peers`——peers 失败走 Debugf 被默认级别过滤，但 close/heartbeat 失败是 Warnf 必然落盘。
- **放大因素**：`internal/logger/logger.go:133-161` 是纯 `fmt.Sprintf` 写文件，**不集成** `observability.RedactingHandler`（该 handler 仅被 observability 包自己的测试使用），脱敏完全依赖调用点自觉。
- **修复**：
  1. 服务端审计字段改为 `path_template`（如 `/rooms/:code/peers`）；如需关联排查，记录 `roomcrypto.Hash(code)` 的前 8 字节 hex；
  2. 客户端 `logFailure` 对 `detail` 也调用 `observability.Redact`；`TransportError.Error()` 改为固定文案 + `Unwrap` 供 `errors.Is/As`，不内嵌含 URL 的 cause 文本；
  3. 治本：`logger` 包写盘前统一过 `observability.Redact`，把红线从"调用点自觉"变为"机制保证"（正则需补 `owner[-_ ]?token|pat`，见中級 C9）。

---

## 2. 高

### H1. NAT 探测系统性误判——全锥/受限锥几乎必然判为 Symmetric，Open 永不命中

- **位置**：`internal/natdetect/stun.go:41` + `internal/natdetect/detector.go:25,35,41,48`
- **问题**：`sendSTUN` 每次 `net.DialUDP("udp", nil, addr)` 新建 socket，两次探测（stunA/stunB）使用**不同本地端口**。RFC 3489 的对称 NAT 判定要求同一内部 endpoint 向两个服务器发包比较映射；当前实现下 `ma1.Port != ma2.Port` 几乎恒真（全锥 NAT 对不同内部端口也分配不同映射端口）→ 误判 `Symmetric`。`detector.go:35` 的 Open 判定同理失效：`getLocalAddr()` 用第三个 socket（DialUDP 8.8.8.8:53），其端口与探测 socket 无关。组网建议系统性失真（对称 NAT 文案会引导用户放弃 P2P）。另：`DetectWithTimeout` 的 `ctx` 参数未传入 `sendSTUN`（死参数，实际超时为 2×3s 固定）。
- **修复**：`Detect` 内创建单个 UDP conn，复用该 conn 向 stunA/stunB 分别发送 binding request（`sendSTUN(conn, server)`），`laddr` 取该 conn 的 `LocalAddr()`；把 ctx 接上。

### H2. `SetMode` 模式互斥不识别 express 连接态——切换模式不退出房间

- **位置**：`internal/webui/app.go:730`（关联 `frontend/src/App.jsx:461-478`）
- **问题**：`connected := a.state == StateConnected || a.state == StateConnecting` 只反映经典模式状态机；express 连接状态在 `ExpressController`/`session.Service` 内，`a.state` 恒为 `StateDisconnected`。express 在房时切到 classic，`connected=false` → `a.express.Disconnect()`（741 行）不执行：daemon 留在房间、房主心跳 goroutine 继续跑、WireGuard 接口存活，而 UI 已切走。前端已禁用切换作缓解（commit b49b53f），但 Wails 后端绑定裸露，前端 bug 或未来 UI 改动可绕过。
- **修复**：`oldMode == ModeExpress` 时以 `a.express.GetState()` 判定占用（State 非 `NoRoom`/`RecoverableError` 即视为在房），或无条件调 `a.express.Disconnect()` 并保证 `session.Service.Disconnect` 无房间时无害 no-op。

### H3. 提权进程内通过 PATH 解析 `powershell.exe`——UAC 提权劫持面

- **位置**：`internal/nbdaemon/artifact_windows.go:98`
- **问题**：`authenticodeSubject` 由 sogame-helper（提权进程）经 `installer.Execute → Verify` 调用，`exec.CommandContext(ctx, "powershell.exe", ...)` 依赖 PATH 解析。UAC 提权进程继承同用户环境（含用户 PATH），同用户非提权进程可在 PATH 前置可写目录放置同名 `powershell.exe`，借一次正常的"修复 NetBird"获得管理员代码执行。AGENTS.md 红线要求系统程序用 System32 绝对路径——`msiexecPath()`（install_windows.go:38-43）已合规，PowerShell 是漏网之鱼。
- **修复**：与 `msiexecPath()` 同款处理，用 `GetSystemDirectory()` 拼 `WindowsPowerShell\v1.0\powershell.exe` 绝对路径，拒绝 PATH 解析。

### H4. 提权进程向用户可写目录写 result/log 文件——跟随符号链接可致本地提权任意文件写

- **位置**：`cmd/sogame-helper/main_windows.go:135` + `internal/nbdaemon/install.go:139`（`remove.go:68` 同）+ `internal/nbdaemon/elevation_windows.go:107`
- **问题**：result/log 路径为 `%LOCALAPPDATA%\SoGame\NetBird\netbird-repair.{result,log}`，该目录当前用户完全可写。同用户非提权进程可预先将 `netbird-repair.result` 创建为指向任意系统文件的符号链接/reparse point：helper 提权后 `os.WriteFile` 跟随链接写入；`msiexec /l*v <logPath>` 同样跟随。`launchElevation` 前的 `os.Remove(resultPath)` 只收窄窗口，不能根除（删除后写入前可重建链接）。`prepareElevation` 对 helper 自身的 reparse 检查（elevation_windows.go:64-74）正是同款防护，结果文件应对齐这一标准。
- **修复**：helper 端写入前 `os.Lstat` + reparse point 检查，删除后以独占方式重建并复查；或将 result/log 落到仅管理员可写的目录（如安装目录）。

### H5. `helperSha256` 校验链整体空转

- **位置**：`internal/releasebuild/netbird-release.json` + `scripts/build-all.ps1` + `internal/nbdaemon/elevation_windows.go:79-83`
- **问题**：`prepareElevation` 支持对 sogame-helper.exe 做 SHA256 校验，但 JSON 中没有 `helperSha256` 字段 → `metadata.HelperSHA256 == ""` → 校验永远跳过；且 `build-all.ps1` 先 `wails build`（SoGame.exe 嵌入 JSON）后构建 helper，即使想填也填不进去。后果：helper 完整性只剩"绝对路径 + 非符号链接"检查；`installer/sogame.iss:57` 允许 `PrivilegesRequiredOverridesAllowed=dialog`（用户可选安装到用户可写目录），此场景下同用户进程可替换 sogame-helper.exe，UAC 弹窗显示的名字不变，用户难以察觉。
- **修复**：调整构建顺序——先构建 helper → 计算 SHA256 → 写入待嵌入的 JSON → 构建 SoGame.exe；或对 helper 增加 Authenticode 签名校验（与 MSI 同级）。短期不实现则删除 `HelperSHA256` 死代码，避免"已有防护"的错觉。

### H6. updater 空 SHA256 静默放行——更新链信任锚断裂

- **位置**：`internal/updater/updater.go:119`（`if expectedSha256 != ""` 空值直接放行）+ `internal/updater/types.go:18`
- **问题**：`PerformUpdate(downloadURL, sha256sum)` 的 hash 由前端回传，整个更新链的信任锚只有这一个哈希（解压后文件无二次校验，新 exe 启动前无签名验证）。空 hash 时下载任意内容都会被解压并（修复 B1 后）替换安装文件。
- **修复**：`Download` 对空 `expectedSha256` 直接报错；`Check` 对 manifest 缺 `sha256` 字段视为格式无效。

### H7. `X-Forwarded-For` 取首值——生产部署下限流与审计 IP 可被任意伪造

- **位置**：`server/room-api/internal/httpapi/server.go:312-323`；关联 `server/room-api/README.md:66-67`、`deploy/traefik-dynamic.yml:42-48`、`server/room-api/deploy/prep-room-api.sh:58`
- **问题**：traefik 默认把真实对端 IP **追加**到 XFF 末尾，保留客户端自带首值（`X-Forwarded-For: 6.6.6.6` 经 traefik 后变为 `6.6.6.6, <真实IP>`）。`clientIP` 取首值即取到伪造值：攻击者每次请求换一个 XFF 首值即可绕过全部 per-IP 限流（建房 5/min 形同虚设），审计 `remote` 字段同步失真。README 已意识到此陷阱（"确保代理会覆盖而非追加"），但 `traefik-dynamic.yml` 没有任何实现该要求的配置，`prep-room-api.sh` 却直接写入 `ROOM_API_TRUST_PROXY=true`——文档要求与部署现实不符。
- **修复**：单跳 traefik 场景改为取 XFF **最右值**并在注释/README 写明"恰好一跳可信代理"假设；或在 traefik 侧加 middleware 清掉入站 XFF。两者选一并落地到 `traefik-dynamic.yml`。

### H8. room-api `Create` 补偿清理不一致——一处误删复用资源，多处泄露 SetupKey

- **位置**：`server/room-api/internal/rooms/service.go:225-229` vs `196-223`
- **问题**：
  1. service.go:228：Policy 已建、第二次 `UpdateExternalIDs` 失败时，`_ = s.nb.DeleteGroup(ctx, group.ID)` **没有 `createdGroup` 判断**——若 group 是 `findGroup` 复用的既有资源（上次失败的残留），此处会删掉不属于自己的 group。与 206/213/221 行的条件式删除自相矛盾，属明显笔误。
  2. SetupKey 创建成功后的所有失败路径（Seal 失败、第一次 `UpdateExternalIDs` 失败、`CreateRoomPolicy` 失败）都只 `RevokeSetupKey` 不 `DeleteSetupKey`——NetBird 账号残留大量 revoked key 垃圾（对比 `teardownRoom` 是 Revoke+Delete 双管齐下）。
- **修复**：228 行补 `if createdGroup`；把"Revoke+Delete setup key、按 createdGroup 条件删 group"抽成 `compensateProvisioning(group, key, createdGroup)` 辅助函数，四条失败路径统一调用。

### H9. 幂等键 error 重试无原子 CAS——并发重试同一幂等键会双重建房

- **位置**：`server/room-api/internal/rooms/service.go:157-169` + `internal/store/store.go:179-182`
- **问题**：`ResetOperation` 是无条件 `UPDATE ... SET status='creating'`。两个并发请求同时看到 `status='error'` → 都 Reset 成功 → `BeginOperation`（`INSERT OR IGNORE` 对已存在行返回 `created=false`）→ `!created && !retryOperation` 为 false → **两个请求都继续执行完整建房流程**，产出两个房间、两份 NetBird 资源，后一个 `SaveOperation` 覆盖前一个。客户端网络抖动自动重试与用户手动重试叠加即可触发。
- **修复**：`ResetOperation` 改为 `UPDATE ... SET room_id=?, status='creating' WHERE idempotency_key=? AND status='error'`，检查 `RowsAffected==1`，抢不到的返回 `ErrOperationInProgress`；补并发测试。

### H10. `Create` 尾部 `SaveOperation`/`Seal` 失败留下"active 房间 + creating operation"——`Reconcile` 重启时误删活跃房间资源

- **位置**：`server/room-api/internal/rooms/service.go:231-243`（失败直接 `return`，不走 `fail()`）+ `service.go:262-295`（`Reconcile` 处理 creating operation 时不检查 `room.Status`）
- **问题**：`SetStatus('active')` 已成功、`SaveOperation` 失败（DB 瞬态错误）→ operation 停留 `creating` → 下次启动 `Reconcile` 按 operation 找到该房间，**不看 room.Status 已是 active**，直接删 Policy/SetupKey/Group 并置 error——一个正在使用的房间被杀。触发窗口小但后果是生产事故级。
- **修复**：`Reconcile` 处理 creating operation 时增加分支：`room.Status=='active'` 说明建房已完整成功，只把 operation 标记为 error（让客户端重试建新房间），**不动 NetBird 资源**；仅 `room.Status=='creating'` 时才执行资源清理。

### H11. 数据卷属主不一致——按现行 compose+prep 脚本部署，容器内 SQLite 无法写入，服务起不来

- **位置**：`deploy/docker-compose.room-api.yml:35-37` + `server/room-api/Dockerfile:24-27` + `server/room-api/deploy/prep-room-api.sh:14-16`
- **问题**：镜像内 `/data` 由 `WORKDIR` 以 roomapi（uid 10001）创建，属主正确；但 compose 用 bind mount `/root/room-api-data:/data` 覆盖了它——`prep-room-api.sh` `mkdir -p` 出的目录属主是 root（755），容器内 uid 10001 对 `/data` 无写权限，`store.Open` 创建 db 文件失败 → 启动即崩溃循环。README.md:39 只说"容器以非 root 用户运行"，未提示需要 `chown`。
- **修复**：`prep-room-api.sh` 增加 `chown -R 10001:10001 "$DATA_DIR"`（注释说明与 Dockerfile 的 uid 对应关系）；README 部署章节补充；compose 增加显式 `user: "10001:10001"` 使意图自文档化。

### H12. "对端 30s 超时转 Reconnecting"判定不检查 peer.State——打洞失败永不超时

- **位置**：`internal/session/service.go:383-389`
- **问题**：`updatePeerWait` 的 `progressing` 判定是"daemon peers 中存在房间成员 IP"，**不检查 `peer.State`**。daemon 同步到 peer 条目后（哪怕永远 `PeerConnecting`，即 ICE 打洞失败的典型现场），计时立即清零，永不超时，状态停在 `ConnectingPeer` 不转 `Reconnecting`。函数注释（375-377）与 AGENTS.md §3（"对端连接超时 30s 转 Reconnecting"）都指向"未 connected 应计时"。现有测试 `robustness_test.go:53` 只覆盖"daemon 完全无该成员条目"的场景。
- **修复**：`progressing` 要求 `peer.State == clientnetbird.PeerConnected`；补"daemon 有条目但持续 connecting 超 30s → Reconnecting"测试。

---

## 3. 中

### 客户端 session / webui

| # | 位置 | 问题 | 修复建议 |
|---|---|---|---|
| C1 | `internal/webui/express.go:399-405` | webui 层把校验类错误也强置 `RecoverableError`、`ConnectedPath=none`。`session.failValidation`（service.go:999-1003）明确"返回错误但不改变会话状态"，但 `ErrSwitchConfirmationRequired`、`ErrRoomAlreadySaved`、昵称校验失败等会把正在 `ConnectedP2P` 的 UI 打成错误态（靠下一次 5s 轮询自愈） | 失败分支用 `snapshot.State/Path` 刷新展示状态，仅当 `snapshot.State == StateRecoverableError` 时才呈现错误态 |
| C2 | `internal/session/service.go:827-830` | `requireEmptyStorage` 过期房间清理漏清 owner token。后果链：旧房主房过期 → Join 新房（Join 响应无 owner token 不覆盖）→ 残留 token 使 `isOwner()==true` → UI 误显示"解散房间"；Reconnect 以旧 token 对新房起心跳（403 后自愈）；Leave 时多发一次必然 403 的 CloseRoom | 过期分支补 `if s.tokens != nil { _ = s.tokens.Clear() }`；`expiry_test.go` 补"房主房过期后 Join 新房 isOwner=false"用例 |
| C3 | `internal/session/service.go:568-574` | `Switch` 无法处理过期房间：`loadSavedRoom` 返回 `ErrRoomExpired` 直接 `failValidation`，而 Create/Join 路径（`requireEmptyStorage`）会自动清理过期房放行。过期房用户必须先手动 Leave 才能 Switch，行为不一致 | `errors.Is(err, ErrRoomExpired)` 时走 `forceClear` 后继续 enroll；补 Switch+过期房测试 |
| C4 | `internal/session/repair.go:57` | 冷却逻辑用真实时钟（`time.Since(s.lastRepairAttempt)`），而 `Service` 已有可注入的 `s.now()`；`repairCooldown`（2 分钟）边界、"修复失败后冷却期内不重复触发"无测试 | 改用 `s.now()`；补冷却边界测试 |
| C5 | `internal/webui/express.go:629, 626` | 错误映射兜底吞语义：所有未匹配错误（含 Disconnect/Leave 失败、启动组装失败）一律显示"加入房间失败"；用 `strings.Contains(err.Error(), "room session is unavailable")` 匹配脆弱 | 导出 sentinel error 用 `errors.Is`；兜底文案按命令上下文区分 |
| C6 | `internal/session/service.go:957` | 心跳退避注释"60s→120s→240s，封顶 2 分钟"与实际（60→120→120，240 在赋值前已被钳到 120）不符 | 改注释或改实现，二者取一 |

### 客户端 updater / 提权 / main

| # | 位置 | 问题 | 修复建议 |
|---|---|---|---|
| C7 | `internal/updater/updater.go:129-168` + `internal/webui/app.go:641` | 解压目录 `%TEMP%\sogame-update` 固定且 `Extract` 前无 `RemoveAll`（上次残留会混入安装目录）；路径前缀检查对 `destDir` 本身是 junction/symlink 的情况无效（同用户可预置 junction 使解压落出目录外）；下载与解压均无大小上限，manifest 的 `size` 字段读取但未用于校验（zip 炸弹可耗尽磁盘）。**当前因 B1 存在无实际危害，但修复 B1 时必须同步处理，否则直接变成可利用** | `Extract` 前 `RemoveAll(destDir)` 并重建；对 `destDir` 做 reparse point 检查；`Download` 校验 `ContentLength` 与 manifest size 并设硬上限；解压累计字节数设上限 |
| C8 | `main.go:91-117` | 更新应用健壮性差：固定盲等 5 秒父进程退出（循环体内无检查，等价于一次 Sleep(5s)，杀软场景可能不够）；任一文件复制失败仅 warning 后继续并照常启动——可能留下新旧混合的安装目录无回滚；`cmd.Start()` 返回值未检查；无日志初始化，失败完全静默 | 循环改为轮询目标 exe 是否可独占打开（带总超时）；关键文件复制失败时中止且不启动新进程；向 result 文件或 Windows 事件日志落一条结果 |
| C9 | `internal/observability/redact.go:30` + `internal/logger/logger.go:133-161` | 脱敏设施未接入主日志路径：`NewRedactingHandler` 生产零调用；`credentialAssignment` 正则有 `admin[-_ ]?token` 但**缺 `owner[-_ ]?token` 与 `pat`**（owner token 恰是 §7 红线对象）；`sensitiveKeys` 缺 `owner_token`。新增日志点误打即泄露 | `logger` 输出函数统一过 `observability.Redact`（或全局换 `RedactingHandler`）；正则与 `sensitiveKeys` 补齐 |
| C10 | `internal/nbdaemon/elevation_windows.go:178-185` | 提权结果文件读取竞态：主进程每 300ms 轮询 `os.ReadFile`，helper 侧 `os.WriteFile` 非原子（创建→截断→写入→关闭）；读到部分写入的 JSON 时 `parseElevationResult` 返回错误且**立即终止等待**——helper 实际成功也被报告为失败，引导用户重试不必要的提权安装 | helper 端改为写临时文件 + 原子 rename（securestore 的 `replaceFile` 是现成实现，可提取共用）；或主进程端解析失败继续轮询直到超时 |
| C11 | `internal/webui/app.go:764-767` + `internal/config/config.go:442-452` | 默认值变体绕过存空判定重新固化：`NormalizeRoomAPIURL` 只对匹配 key 做归一化，返回值仍是原值。用户输入 `https://legengen.top/`（带斜杠变体）不命中废弃名单 → 判等失败 → 变体固化落盘——正是本分支要消灭的 drift 换马甲。supernode 判等（app.go:412）对 hostname 大小写变体同理（当前默认是 IP 无此问题，未来换域名即有） | `NormalizeRoomAPIURL` 返回归一化后的值（去尾随斜杠）；或判等统一用归一化 key；supernode 判等改 `strings.EqualFold` |
| C12 | `internal/webui/app.go:758-775` | `SaveExpressSettings` 不热更新运行中的 ExpressController：Room API 客户端在 `NewApp`（app.go:87）一次性构造并固化 URL；保存只落盘，运行中 controller 仍用旧地址需重启生效；`GetMode` 读配置返回新值，UI 呈现"已生效"假象 | `ExpressController` 增加 `SetRoomAPIURL`（重建 `roomapi.Client` 并经 `Configure` 注入）；或保存成功时返回"需重启生效"提示 |
| C13 | `internal/config/config.go:153-158` | .bak 恢复路径两个边缘问题：(a) `migrateAndPersist→Save` 会把损坏的 `config.yaml` 覆盖备份到 `.bak`（好备份被坏文件顶替）；(b) 恢复的配置无废弃值时不 Save，损坏的 `config.yaml` 不自愈，每次启动重复走恢复路径；`RestoreFromBackup` 恢复的 backupCfg 不过 `Validate` | 恢复成功后无条件 Save 一次；先 `Validate` backupCfg 再采用 |

### room-api 服务端

| # | 位置 | 问题 | 修复建议 |
|---|---|---|---|
| C14 | `internal/rooms/service.go:171-183, 231-233` | `Create` 前段多个失败点（`Seal(code)`、`CreateRoom`、`SetOwnerTokenHash`、`SetStatus('active')`）未走 `fail()`，失败后 operation 停留 `creating`，同幂等键重试永远 409 `ErrOperationInProgress`，只能等进程重启由 `Reconcile` 兜底 | 统一改为 `return s.fail(ctx, roomID, idempotencyKey, ...)` |
| C15 | `internal/rooms/service.go:505-508` + `cmd/room-api/main.go:113-116` | `closing` 状态泄漏：teardown 先置 `closing` 再做多次外部调用，进程在窗口内退出（发版/OOM）则房间永久停留 `closing`：`Join`/`Peers`/`Heartbeat` 全部 410，但 NetBird 资源仍在、无任何自动恢复路径（`Reconcile` 只看 creating operation 和 stale active，看门狗只扫 active） | `Reconcile` 增加对 `closing` 状态房间的续跑（重跑 `teardownRoom`，配合 B2 的 404 容忍后幂等） |
| C16 | `internal/httpapi/server.go:335-383` | 限流器 `clients` map 无界增长 + 5 分钟清扫间隔：每个新 client 字符串插入一条目，叠加 H7（XFF 可伪造）或 IPv6 前缀轮换，单攻击者可在 5 分钟内注入百万级条目（全局 mutex 下还会拖慢所有请求）——内存 DoS 向量 | 清扫间隔降到 1 分钟；`clients` 设容量上限（如 100k，超限拒绝新 IP 或触发全量清扫）；根治依赖 H7 修复 |
| C17 | `internal/netbird/client.go:210-213` | `RevokeSetupKey` 的 PUT body 只有 `{"revoked": true, "auto_groups": [...]}`——NetBird Management 的 `PUT /api/setup-keys/{id}` 是整体替换语义，可能把 `name`/`type`/`expires`/`usage_limit` 重置为零值（吊销本身有效，但审计面元数据丢失；不同版本行为有差异） | PUT 前先 GET 取回完整对象，改 `revoked` 后整体回写；或按 v0.74.7 实测确认容忍行为并在注释中固化结论 |
| C18 | `internal/rooms/service.go:302-316`（对比 527-553） | `Reconcile` 的 stale 清理不踢 peer，与 `teardownRoom` 不对称：若组内还有 peer，NetBird 可能拒绝删非空 group（清理静默失败但房间已 disable），或 peer 残留账号占用名额 | stale 清理复用 `teardownRoom`（传 reason="stale_reaped"），消除分叉 |
| C19 | `internal/rooms/service.go:596-598` | `Disable` 把内部错误吞并成 404：`if errors.Is(err, store.ErrNotFound) \|\| err != nil` 恒等于 `err != nil`，DB 内部错误也映射为 `ErrInvalidRoom`→404。违反 AGENTS.md"不得把内部错误与资源不存在混为同一 404" | 拆开两个分支：`ErrNotFound`→`ErrInvalidRoom`，其余原样上抛 |
| C20 | `internal/httpapi/server.go:107, 114, 141, 179, 200, 228` | 429/503 响应未带 `Retry-After` 头——客户端 roomapi 包实现了"Retry-After 钳制"（AGENTS.md §4），服务端不发该头，瞬时限流时重试节奏不可控 | 429 统一带 `Retry-After: 5`（或按窗口剩余秒数），503 带 `Retry-After: 1` |
| C21 | `internal/config/config.go:71` + `README.md:58` + `AGENTS.md §3` + `service.go:297` | 房主离线超时口径三处不一致：代码默认 5m / README 5m / AGENTS.md §3 写 30 分钟 / `Reconcile` stale 用 30 分钟。客户端心跳间隔 60s，5m 超时意味着房主 3 次心跳丢失即解散房间（移动网络切基站即可触发）；运维按文档预期会误判 | 确认产品意图后统一三处口径；若 5m 是有意的，修正 AGENTS.md §3 |
| C22 | `internal/store/store.go:36/87/185` + `internal/rooms/service.go:171-176` | `rooms.code_ciphertext` 是无消费点的死字段：全库只有写入没有读取（幂等重放实际走 `operations.response_ciphertext`）。README 声称它用于"幂等重放还原下发"是过时描述；AGENTS.md §3"房间码仅存 SHA256 哈希"与实现不符。密钥与 DB 同机存放，该密文防护等价于明文，白白扩大泄露面 | 删除该字段（DDL+struct+INSERT）并修正 README；如确需保留，在 AGENTS.md/README 如实更新安全模型描述 |
| C23 | `internal/rooms/service.go:594-615` | admin `Disable` 不删 group、不踢 peer：只 Revoke key + 删 Policy + 标记 disabled，group 与组内 peer 残留 NetBird 账号，与 `teardownRoom` 语义不一致 | 明确语义：要么复用 `teardownRoom`，要么加注释并在 README 说明残留资源由谁清理 |

---

## 4. 低

### 客户端

| # | 位置 | 问题 |
|---|---|---|
| L1 | `internal/session/peers.go:96-134` | 托盘 30s 降频未接线：`PeerRefresher.Watch`/`PeerRefreshTray` 仅被测试引用，生产路径是固定 5s 刷新（express.go:650）。AGENTS.md §4"前台 5s/托盘 30s"中的托盘降频未实现，`Watch` 属死代码。接线或删代码修文档，二者取一 |
| L2 | `internal/webui/express.go:40-42, 56` | `ExpressState` 注释与实现矛盾（声称"敏感字段序列化时脱敏"，但 `refreshRoomView` 每 5s 把明文房间码写入 `state.RoomCode` 广播——功能本身合理，前端依赖此字段）；56 行 `RoomCode` 注释"用户已主动断开"错位自 `Disconnected` |
| L3 | `internal/session/service.go:292` | `View` 的 room_closed 收尾 `forceClear(context.WithoutCancel(ctx))` 无 deadline，daemon 僵死时内部 gRPC（无 per-call 超时）可长期阻塞 View 轮询 goroutine。建议 `context.WithTimeout(WithoutCancel(ctx), cleanupTimeout)` |
| L4 | `internal/session/service.go` / `express_windows.go:120` | 重组装不停旧心跳：`waitForDaemonAndReassemble → assembleWindowsExpress` 新建 `session.Service` 替换旧实例，但旧实例的房主心跳 goroutine 无停止入口（Service 无 Close/Stop），窗口期内可能双心跳（同一 DPAPI 凭证文件，Leave 后自愈） |
| L5 | `internal/webui/express.go:520-521` | `EventsEmit` 在 `c.mu` 临界区内发送。当前 Wails EventsEmit 非阻塞风险低，建议把 emit 移到解锁后，避免未来 Wails 行为变化引入持锁 IO |
| L6 | `internal/webui/express.go` | ticker 发起的旧 `refreshRoomView` 若晚于命令完成返回，会用旧快照覆盖刚设置的新状态（5s 后自愈）。可在写回前比较 `view.Session.Revision` 或 busy 标志 |
| L7 | `internal/webui/express.go:154-156`、`express_other.go:46` | 死代码：`hasError()`（且无锁读 `c.state`）与 `errExpressUnsupported` 无调用者 |
| L8 | `internal/nbdaemon/elevation_windows.go:106, 124` | `launchElevation` 内部硬编码 `context.Background()`，UI 取消或应用退出无法中断最长 10 分钟的轮询。建议签名加 `ctx` 透传 |
| L9 | `internal/nbdaemon/elevation_windows.go:114-117` | 四处 `windows.UTF16PtrFromString` 错误全部忽略。固定字符串与已校验路径实际不会失败，但应检查或注释说明 |
| L10 | `internal/nbdaemon/artifact_windows.go:64-74` | 吊销检查不可达时降级放行。离线可用性是合理设计且有 Warn 日志，但建议把"吊销未验证"状态传到诊断/UI 可见层 |
| L11 | `internal/releasebuild/metadata.go:51, 55-58` | `CertificateThumbprintAtRelease`、`Install.Executable`、`Install.Arguments` 是死字段，元数据与代码各自维护同一事实，漂移无告警。要么消费要么删除 |
| L12 | `internal/netbird/version.go:28` | `ExpectedVersion = "0.74.7"` 硬编码，与 `netbird-release.json` 的 `version` 双真相源，升级 NetBird 时漏改任一处即误判。建议从 `releasebuild.Load()` 派生 |
| L13 | `internal/netbird/rpc_adapter.go:183` | `string(request.SetupKey.value)` 产生的 string 副本无法清零（proto 字段限制），且 `Enroll` 会销毁调用方的 `SetupKey`（defer Clear）——"Enroll 消费并销毁 SetupKey"的语义应在 `EnrollmentRequest` 注释中明示 |
| L14 | `internal/nbdaemon/hide_windows.go:58-70` | `shortcutDirectories` 中 `if dir := filepath.Join(os.Getenv("PUBLIC"), "Desktop"); dir != ""` 永真（环境变量为空时 `Join` 返回 `"Desktop"`）。实际无害（ReadDir 失败跳过），逻辑应先判环境变量非空 |
| L15 | `internal/securestore/metadata.go:131`、`roomcode.go:106`、`ownertoken.go:107` | Windows 上 `Chmod(0600)`/`MkdirAll(0700)` 仅影响 read-only 属性不约束 ACL；实际保护依赖 DPAPI + 用户私有目录 ACL。行为正确，建议注释说明以免读者高估其作用 |
| L16 | `main.go:44-50` | 引入 `flag.Parse` 后正常启动携带未知参数会 exit 2（此前忽略），回归面极小但存在；`--update-apply` 与正常启动隔离良好 |
| L17 | `internal/config/config.go:252` | `Save()` 无条件触发 `getEncryptor()`：即使 `cfg.Key == ""` 也会初始化加密器并读写 `.key` 文件。未来新增调 Save 的测试一旦漏隔离环境变量即触真实用户目录。建议加测试注入点或注释强调 |
| L18 | `frontend/src/App.jsx:34` | `SaveExpressSettings` 仅 import 无调用：Room API URL 无 UI 设置入口，与 AGENTS.md §6.1"可在 UI 设置或配置文件中临时指向本地 Mock"描述不符。接线或修文档 |
| L19 | `internal/natdetect/{detector,stun,types}.go` | 三个新文件无 SPDX license 头，与本分支统一加头规范不一致 |
| L20 | `internal/security/encryption.go`（既有问题，非本分支引入） | 以 base64 文本截断作 AES key（未解码）、CFB 无完整性校验、密钥与密文同目录；另有 `Connect` 锁外读 `a.cfg.Key`（app.go:427）、`GetMode` 锁外读 `a.cfg.NodeName`（app.go:713）、`ConfigCache.GetCached` 无生产调用点（死代码）。本分支仅加 license 头未恶化，记录备查 |

### room-api 服务端

| # | 位置 | 问题 |
|---|---|---|
| L21 | `internal/rooms/service.go:181-183` | `SetOwnerTokenHash` 冗余调用（`CreateRoom` 的 INSERT 已写入 `owner_token_hash`，store.go:185），可删 |
| L22 | `internal/rooms/service.go:492-499, 458` | sweep 投递循环在 `sweepCtx.Done()` 后无 break（无害但不精确）；局部变量 `rooms` 遮蔽包名 |
| L23 | `internal/config/config.go:67` + `server.go:136/281` | `MaxBodyBytes` 未校验 >0：设 0 时 `joinRoom` 的 `ContentLength>0` 预检会 413 拒绝一切，而 `decodeJSON` 兜底 4096，行为分裂。建议 `Load` 中校验 `MaxBodyBytes >= 256` |
| L24 | `internal/store/store.go` | `operations`、closed/disabled 房间无 TTL 清理；幂等响应密文（含 setup key/owner token）永久保存。建议定期清理（operations 24h、closed 房间 30 天） |
| L25 | `internal/store/store.go:67-78` | `sql.DB` 未 `SetMaxOpenConns(1)`，高并发写依赖 `_busy_timeout=5000` 兜底。建议显式设置（SQLite 惯用做法） |
| L26 | `deploy/docker-compose.room-api.yml:37` | bind mount 宿主机 `/etc/pki/tls/certs/ca-bundle.crt` 到 Debian 容器：镜像已装 ca-certificates 属冗余；宿主机缺该文件时 Docker 会创建空目录遮蔽镜像 CA，导致 Management HTTPS 全部 TLS 失败。建议删除该挂载 |
| L27 | `internal/config/config.go:134-146` | 密钥派生三态无实际歧义（32 字符 base64 解码为 24B≠32B，分支不重叠），但 SHA256 单轮派生对弱口令无强化；base64 解码成功但非 32B 时静默落到后续分支，运维易混淆。文档应强调必须高熵（`openssl rand -base64 32`），非 32B 的合法 base64 建议报错而非静默降级 |
| L28 | `internal/rooms/service.go:149-152, 175` | 房间码 `code_hash` UNIQUE 冲突时整个 Create 失败（概率 ~2^-60 级）。可忽略；或捕获 UNIQUE 冲突重生成一次 |
| L29 | `internal/rooms/close_test.go:315-330` | `TestCloseTeardownFailureKeepsActiveAndRetryable` 名不副实：fake 无法注入 `DeletePolicy` 失败，测试体实际只验证了正常 Close，给出虚假的失败路径覆盖感。给 fake 加 `failDeletePolicy` 开关并补真实断言，或删除该用例 |
| L30 | `server/room-api/README.md:47` | 声称 `ROOM_API_ADMIN_TOKEN` 必填，实际为空即禁用端点（config.go:63、server.go:271-273）。修正文档 |
| L31 | `internal/netbird/version.go:197` | `Subscribe` 中 `errors := make(chan error, 1)` 遮蔽 `errors` 包名，风格瑕疵 |

---

## 5. 缺失测试（重点）

| 模块 | 现有覆盖 | 缺口 |
|---|---|---|
| `internal/updater` | **零测试** | `compareVersion` 边界（"2.10" vs "2.9"、"1.0-beta"、不等长段）、`Extract` 的 ZipSlip 用例（`../`/绝对路径/junction）、`Download` 哈希不匹配/空哈希。**B1 这种全链路失效若有任一集成测试即可暴露** |
| `internal/webui`（express 层） | **零测试**（express.go 663 行 + express_windows.go 258 行；app_test.go 仅加许可证头） | `expressPublicError` 的 14 个映射分支、`runCommandWithInitialState` 的 BusyCommand 互斥与"校验错误不改状态"、`refreshRoomView` 的 `ErrRoomClosed` 分支、`chooseMSIAction` 选择逻辑 |
| room-api `internal/crypto` | 零测试 | Seal/Open 往返、篡改密文、错误密钥、截断密文 |
| room-api `internal/store` | 零测试 | migrate 幂等性、`ensureRoomColumn` 增量加列、`BeginOperation`/`ResetOperation` 并发（H9 的 CAS 修复必须配测试）、NULL 时间字段扫描 |
| room-api 限流器 | 零测试 | 窗口计数、窗口滑动重置、sweep 清理、429 响应 |
| room-api Create 补偿路径 | fake 只有 `failDeletePeer`/`failListPeers` 两个开关 | 不支持注入 `CreateGroup`/`CreateSetupKey`/`CreateRoomPolicy` 失败——H8 的四条补偿路径（含 228 行误删 bug）现有测试体系**结构上无法发现**，需扩展 fake 后逐路径断言清理调用序列 |
| room-api `Reconcile` | 零测试 | creating 清理、stale 清理、H10 的 active 房间保护分支、C15 的 closing 续跑 |
| `internal/session/repair.go` | 触发与防误触发已覆盖 | 冷却逻辑用真实时钟（C4），2 分钟冷却边界无测试 |
| `cmd/sogame-helper` / elevation | 零测试 | `prepareElevation` 的符号链接/reparse 拒绝、`parseElevationResult` 边界均为纯逻辑，Windows runner 上可测 |
| `internal/releasebuild` | 零测试 | 建议一个"嵌入 JSON 可解析且 version/SHA256/ProductCode 非空"的冒烟测试，防发布时 JSON 与代码漂移 |
| `internal/securestore/ownertoken` | 无独立用例 | 与 roomcode 结构同构；建议至少补"room.code 与 owner.token envelope 互不兼容（magic 防互换）"一条 |
| `internal/natdetect` | 零测试 | H1 修复后应补"同一 conn 双服务器映射比较"的判定测试（可用本地 UDP fake STUN server） |

---

## 6. 未发现问题的维度（核查通过）

- **MSI 校验链**：大小 + SHA256 + WinVerifyTrust + publisher CN/O 四重全部失败即中止（artifact.go:46-82）；校验→暂存→二次校验→执行的 TOCTOU 收窄设计（install.go:73-84）正确；`syscall.EscapeArg` 逐参数转义（elevation_windows.go:110-113）合规；product code 白名单正则（remove.go:31）防参数注入；`msiexec` System32 绝对路径（install_windows.go:38-43）合规；helper 端 `IsElevated()` 双重确认。
- **room-api SQL 全参数化**；owner token（service.go:571-581）与 admin token（server.go:270-277）均长度预检 + `subtle.ConstantTimeCompare`；setup key 落库 AES-GCM（nonce 前置、rand 生成）实现正确；`MaxBodyBytes` 所有读 body 端点均有 ContentLength 预检 + `http.MaxBytesReader` 兜底。
- **客户端 `SetupKey` 防泄漏四件套**（String/Format/LogValue/MarshalJSON）完备、无绕过；DPAPI current-user scope + entropy + UI_FORBIDDEN 正确；blob 复制后清零再 `LocalFree`；原子替换走同目录临时文件 + `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`；room.json 不含敏感明文。
- **状态机派生规则**（P2P 优先、RelayAllowed=false 忽略中继）实现正确、测试密集（session 包 68 个测试函数）；`beginCommand/endCommand` 全部 7 个调用点 defer 配对，无泄漏无死锁；enroll 事务补偿逆序清理完整；View 高频只读路径未持锁调外部服务。
- **config 迁移链**（本分支亮点）：`LoadOrCreate` 三条加载路径（正常/parse 失败走 .bak/validate 失败走 .bak）均接入 `migrateAndPersist`；消费点回落完整（GetConfig/GetConnectionDetails/GetMode/NewApp/Connect/edge.Start 传生效值副本/邀请码双重 Normalize）——**无"置空后空值直接传给 edge.exe"的漏网路径**；废弃名单 `map[key]bool` 精确匹配 + 归一化，用户自建节点不误伤（`TestMigrateRoomAPIURLPreservesCustom` 覆盖）；迁移/幂等/端到端测试配套到位。
- **经典模式回归面干净**：`nic/tap/platform/poll/diagnostics/logger/security` 仅许可证头；`edge.go` 仅锁内日志竞态修复；TAP 安装包路径 `{app}\installer\tap` 与 `tap.FindDriverDir` 一致。
- **前后端契约**：wailsjs 28 个方法与后端公开方法一一对应；`ExpressRevealRoomCode` 的 `(string, *ExpressError)` 双返回值与前端 Promise/catch 匹配。
- **打包脚本**：`build-all.ps1` 对 MSI 做 SHA256 校验（不匹配即删+throw）；`build-installer.ps1` 预检文件齐全；`publish-update.ps1` zip 哈希入 `update.json`。
- **room-api 其余安全项**：PAT/setup key/owner token 未发现进日志（除 B4 的房间码 path）；方法白名单路由，未匹配一律 404；无 CORS 头（桌面客户端场景无 CSRF 暴露面）；Dockerfile 非 root + 健康检查 + 无密钥硬编码；go.mod 仅 go-sqlite3 一个外部依赖，符合"仅标准库+SQLite 驱动"约束；`randomRoomCode` 32 字符表整除 256 无模偏差；410/404/502 错误三分与客户端契约一致（除 C19）。

---

## 7. 总体评价与建议处理顺序

工程质量整体偏高：状态机测试密集、事务补偿完整、凭证管理规范、MSI 校验链完备、config 迁移链"三步纪律"落地扎实。但四条阻塞项的共同特征是**"防护/链路写了，最后一环没接通"**：热更新从未真实验证过、teardown 幂等假设与实现相反、Reconcile 与看门狗各说各话、脱敏设施未接入日志总线。room-api 侧另有系统性薄弱集中在"状态机迁移与外部资源操作缺乏统一的幂等 + 条件更新原语"（B2/H8/H9/H10 同源）。

建议处理顺序：

1. **第一批（阻塞）**：B1-B4。其中 B2/B3 会改变 room-api 状态机语义，越早定越好；B1 修复后必须手工验证一次完整更新流程。
2. **第二批（信任链与正确性）**：H3-H6（提权/更新信任链同批）、H1/H2/H12（功能正确性）、H7-H11（room-api 生产可用性）。
3. **第三批（随下一迭代）**：中级项 + 测试补齐（优先 updater、webui express 层、room-api crypto/store/补偿路径）。
4. 低级项以文档修正与死代码清理为主，不阻塞合并。

## 8. 附注

- room-api 测试在本机跑不起来（go-sqlite3 经 goproxy 下载超时，CGO 依赖）。考虑到该模块含 CGO 依赖，建议在有网环境（或 CI）至少跑通一次 `cd server/room-api && go test ./...` 再合并。
- 评审基线之后若分支有新提交，本文档结论不自动覆盖新增改动。

---

## 9. 修复状态跟踪（2026-10-05）

以下评审项已在工作区修复并随本次提交入库（与修复提交同批验证）：

| 评审项 | 修复内容 | 验证 |
|---|---|---|
| **B1** 热更新失效 | `PerformUpdate` 改为执行解压目录中的 `SoGame.exe`（启动前校验主程序存在）；`runUpdateApply` 增加源≠目标防御、轮询等待旧进程释放文件锁（60s 超时，替代盲等 5s）、关键文件（SoGame.exe/sogame-helper.exe/edge.exe）失败即中止不启动、结果落 `%TEMP%\sogame-update-apply.log` | `main_test.go` 4 用例，`go test ./...` 通过；**真实端到端更新流程仍建议发版前手工验证一次** |
| **B2** teardown 复活已关闭房间 | `store.BeginClosing` 状态 CAS（`UPDATE ... WHERE status='active'`），CAS 失败幂等返回；`netbird.ErrNotFound` 类型化，删除类调用全部容忍 404；`Close` 对 `closing` 状态继续走 teardown 由 CAS 去重 | `close_test.go` 新增 2 回归用例（过期快照不复活、404 容忍） |
| **B3** Reconcile 误杀 legacy 房间 | `ListStaleActiveRooms` 去掉"从未心跳"分支，与看门狗同一兼容语义；例外由客户端 24h 本地过期兜底 | `close_test.go` 新增 1 回归用例 |
| **B4** 房间码进日志（双侧） | 服务端审计 `pathTemplate`（`/rooms/:code/peers`）；客户端 `logFailure` 的 detail 过 `Redact`、`TransportError.Error()` 固定文案（根因走 `Unwrap`）；`internal/logger` 写盘点统一强制 `Redact`；正则补 `owner[-_ ]?token|pat`、`sensitiveKeys` 补 `owner_token` | `logger_test.go`/`redact_test.go`/`robustness_test.go`/`server_test.go` 新增红线回归用例，客户端测试全通过 |
| **H6** updater 空哈希放行 | manifest 缺 `sha256` 视为无效；`Download` 空哈希直接拒绝 | `updater_test.go` 2 用例 |
| **H10** Reconcile 误删 active 房间 | `Reconcile` 处理 creating operation 时 `room.Status=='active'` 只标 operation 为 error，不动 NetBird 资源 | `close_test.go` 新增 1 回归用例 |
| **C7** 解压目录清理/注入 | `Extract` 前 `RemoveAll` 且拒绝符号链接/junction 目标；下载 512MB、解压 1GB 硬上限；`ContentLength` 完整性校验 | `updater_test.go` ZipSlip/残留清理/符号链接 3 用例 |
| **C8** 更新应用健壮性 | 见 B1 行（同一提交） | 同 B1 |

**验证环境限制**：room-api 的 `rooms` 包测试（含本次 4 个新回归用例）依赖 go-sqlite3（CGO），本机 `CGO_ENABLED=0` 且无 gcc，**未能在本机执行**；`httpapi` 包（含 `pathTemplate` 测试）通过，`go build ./...` 与 `go vet` 干净。合并前请在装有 gcc（MinGW）的环境或 CI 以 `CGO_ENABLED=1` 跑通 `server/room-api` 全部测试。

**同步更新的文档**：AGENTS.md（§3 房主机制修正"30 分钟"为 5m 默认值并补充 CAS/404/legacy 语义、§4 updater 与 logger 职责、§5 room-api 测试 CGO 说明、§6.2 补两个看门狗环境变量、§7 日志红线改为机制保证表述并补审计路由模板约定）、`server/room-api/README.md`（审计日志只记路由模板的安全说明）。

**仍待处理**（评审后续批次，本次未动）：H1（NAT 探测误判）、H2（SetMode 互斥）、H3-H5（提权链：PowerShell PATH 解析/result 文件符号链接/helperSha256 空转——注意 `a2a34e2` 已改 helper 补编译逻辑，修复 H5 前先读该提交）、H7（XFF 首值）、H8/H9（Create 补偿与幂等 CAS）、H11（数据卷属主）、H12（对端超时判定）、其余中低级项。
