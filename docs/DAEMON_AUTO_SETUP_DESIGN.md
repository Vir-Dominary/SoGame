# 守护进程一体化（去按钮化）设计方案

- **日期**：2026-11-17
- **状态**：设计评审稿
- **背景**：测试反馈"守护进程安装/修复按钮仍有问题"；产品决策——不再让用户管理守护进程，使其成为程序内建的一部分。

---

## 1. 现状与根因

### 1.1 当前模型

极速模式依赖 NetBird daemon（官方 MSI 安装的 Windows 系统服务）。当前把它当作"用户可管理的组件"：

```
refreshService 巡检 → ServiceMissing/VersionMismatch/Unhealthy
  → ExpressState.Service.RepairRequired=true
  → 前端显示"守护进程未安装/异常 [安装]/[修复]"按钮（App.jsx:684）
  → RepairService → windowsRepairFunc → UAC → msiexec → waitForDaemonAndReassemble
```

### 1.2 按钮问题的根因（按可能性排序）

| # | 根因 | 证据 |
|---|---|---|
| R1 | **MSI 不在预期位置**：repair 从 `SoGame.exe` 同目录取 MSI（`express_windows.go:216`）。自行构建/绿色版分发若缺 MSI（如本次 GitHub 不可达导致构建缺件），按钮必然报"缺少安装包"，且用户无解救路径 | `nbdaemon.ErrArtifactMissing` |
| R2 | **按钮模型暴露内部细节**：用户需要理解"守护进程"是什么、何时点、失败怎么办。UAC 取消、杀软拦截、Wintun 冲突等失败都以技术文案呈现，无自动接管 | 错误映射 `expressPublicError` |
| R3 | RepairService 状态机复杂：B-1 死锁（已修）、M-W2 互斥（已修）之后，剩余面是"修复中→重组装→巡检回写"的异步时序，前端按钮态与后端巡检竞态仍可能出现按钮残留显示 | `waitForDaemonAndReassemble` 无 ctx、与轮询并发 |
| R4 | 已装 NetBird 但版本漂移（未来 0.74.8+）时显示"修复"，repair 走 REINSTALL 但 ProductCode 已变，可能失败或降级 | `chooseMSIAction` |

### 1.3 结论

按钮问题的本质是**把基础设施的生命周期管理推给了用户**。修复方向不是继续打磨按钮，而是消灭按钮。

---

## 2. 目标与非目标

**目标**

- G1 用户在**任何路径下都不需要感知"守护进程"**：无安装/修复按钮、无该概念的文案。
- G2 标准路径（Inno 安装包）安装完成即可用，**零 UAC 交互**（安装器本身已提权）。
- G3 非标准路径（绿色版、首装失败、被卸载/杀软清除、版本漂移）在**首次使用极速模式时自动补齐**：最多一次 UAC 弹窗 + 进度提示，成功后原命令自动继续。
- G4 失败时给出**用户可理解**的状态与通用的"重试"（不是"修复守护进程"），并保留自动重试（限频）。
- G5 不削弱现有安全链：MSI 四重校验（SHA256/Authenticode/publisher/证书指纹）、helper 白名单、UAC 单动作通道全部保留。

**非目标**

- 不替换 NetBird daemon 为进程内 wireguard-go（重写数据面，数月级工作量，排除）。
- 不引入 SoGame 自有常驻服务（NetBird MSI 自己就是服务安装器，重复造轮子）。
- 不改变 daemon 安装后的运维模型（仍是官方 MSI/服务，可独立卸载——卸载后由 G3 路径兜底）。

---

## 3. 方案选型

| 方案 | 做法 | 评价 |
|---|---|---|
| **A. 安装器预装** | Inno Setup postinstall 静默执行 NetBird MSI（经 sogame-helper 校验链） | 标准路径零交互；业界标准做法（运行库/驱动同模式）。不能覆盖绿色版与装后损坏 |
| **B. 运行时自动就绪** | 首次进入/使用极速模式时自动检测→自动安装→自动继续命令 | 覆盖全部残余场景；代价是首用一次 UAC + 等待 |
| C. 进程内 wireguard-go | 去掉 daemon 依赖 | 排除：重写数据面+控制面同步，工作量与风险不可接受 |
| D. 自研常驻服务管理 daemon | SoGame 自带 Windows 服务 | 排除：增加一个常驻进程与安装面，收益为零 |

**结论：A + B 组合。A 覆盖 90%+ 用户（安装包分发）；B 兜底其余全部场景。UI 层统一为"极速模式就绪状态"，删除守护进程概念。**

---

## 4. 总体设计

### 4.1 两条就绪路径

```
┌─ 安装包用户 ─────────────────────────────────────────────┐
│ Inno Setup 复制文件（含 MSI + sogame-helper）             │
│   → postinstall：sogame-helper --action install --artifact …  │
│     （静默、复用四重校验链、失败不阻断安装、写日志）        │
│   → 用户打开 SoGame：daemon 已就绪，直接可用               │
└──────────────────────────────────────────────────────────┘

┌─ 运行时兜底（绿色版 / 预装失败 / 被卸载 / 版本漂移）─────┐
│ 进入极速模式 或 执行房间命令（Create/Join/Reconnect）     │
│   → EnsureDaemonReady(ctx)：巡检不健康 → 自动修复        │
│     → UAC（一次）→ msiexec → 重组装 → 继续原命令          │
│   → 全程 UI 显示"正在准备极速模式…（首次约需 30 秒）"     │
└──────────────────────────────────────────────────────────┘
```

### 4.2 状态模型（对用户可见的只有四态）

| 对外状态 | 触发 | UI 呈现 |
|---|---|---|
| **就绪（Ready）** | `ServiceReady` | 无任何守护进程相关 UI（现状的"守护进程"区块删除） |
| **待准备（NeedsSetup）** | 启动预检发现 daemon 不健康（静默巡检，不弹 UAC） | 极速模式面板顶部提示条："极速模式需要安装网络组件。[立即准备]"——点击才触发 UAC |
| **准备中（Preparing）** | `EnsureDaemonReady` 进行中 | 极速模式面板顶部进度条："正在准备极速模式…"（创建/加入按钮禁用，带 spinner） |
| **准备失败（Failed）** | 自动修复失败且冷却未到 | 错误条："极速模式暂时不可用：<通俗原因>。[重试]"（通用重试按钮，重走 EnsureDaemonReady） |

内部仍保留 `ServiceInspector`/`ServiceHealth` 全套巡检，但 `RepairRequired` 字段不再驱动任何按钮——它只作为"需要自动修复"的内部信号。

**UAC 触发时机（已确认：决策 C）**：应用启动时后台**静默预检**（只巡检不弹窗）；
不健康则展示"待准备"提示条。UAC 只在两个明确的用户意图点弹出：
① 用户点击"立即准备"；② 用户直接执行房间命令（创建/加入/重连——此时意图明确，
不再要求先点提示条，直接走 §4.4 的透明等待）。

### 4.3 核心组件：`EnsureDaemonReady`

位置：`internal/webui/express_windows.go`（Windows 实现）+ `express.go`（状态呈现）。

```go
// EnsureDaemonReady 保证守护进程可用；不健康时自动修复（含一次 UAC），
// 成功后重组装控制器。与房间命令共用 BusyCommand 互斥（busy="daemon-setup"）。
// 返回 nil 表示就绪；返回错误表示准备失败（已写入 state.Error）。
func (c *ExpressController) EnsureDaemonReady(ctx context.Context) error
```

行为细则：

1. **巡检**：`refreshService` 已填充 `state.Service`；`Health == ServiceReady` → 立即返回 nil。
2. **自动修复触发条件**：`ServiceMissing` / `ServiceVersionMismatch` / `ServiceStopped` / `ServiceUnhealthy`，且距上次自动修复尝试 > 60s 冷却。
3. **互斥**：占用 `BusyCommand="daemon-setup"`；若已有房间命令在跑，等待其完成（最多 30s，`waitForRoomCommandIdle` 短轮询）后占位，等待超时返回"有房间操作正在进行"；若另一 EnsureDaemonReady 在跑，等待其结果并复用。断开/离开属本地清理命令，就绪流程占用期间照常放行。
4. **修复动作**：复用现有 `windowsRepairFunc`（chooseMSIAction → RequestInstallerElevation → waitForDaemonAndReassemble），不改动 helper 与 MSI 校验链。
5. **冷却与上限**：连续失败 3 次后停止自动重试；用户显式点击"立即准备"（ExpressPrepareDaemon）或执行新的房间命令时清零计数重试（避免杀软拦截场景下 UAC 弹窗轰炸，同时保证提示条上的"重试"按钮真实有效）。
6. **就绪后处理**：重组装 + `refreshRoomView`（房间视图恢复）；若本次是"命令前准备"，继续执行原命令。

### 4.4 启动预检与命令路径透明等待

**启动预检**（`ExpressController.Startup` 流程内，静默）：

```go
// 启动时只做巡检不弹 UAC：不健康时置"待准备"提示条状态。
c.refreshService(ctx) // 现有逻辑，已每 5s 轮询
// state.DaemonNeedsSetup = state.Service.Health != ServiceReady
```

**命令路径透明等待**（用户明确意图点）：`ExpressController.CreateRoom/JoinRoom/Reconnect`
入口处统一：

```go
if err := c.EnsureDaemonReady(ctx); err != nil {
    return c.snapshot() // state.Error 已填充"准备失败"
}
// …原有命令逻辑
```

超时预算：`daemon-setup` 单独用 90s（msiexec 首装可能 30s+），不占用房间命令的 30s。
预算经 ctx 贯通到提权等待阶段（`RequestInstallerElevation` → `launchElevation`
→ `awaitElevationResult`）：UAC 挂起/msiexec 卡死时由调用方预算先行截断，
不会以 helper 侧 10 分钟兜底超时长期占用 busy 位。

### 4.5 UI 变更（frontend/src/App.jsx + webui 绑定）

**删除**：

- "守护进程未安装/异常 [安装]/[修复]"区块（App.jsx:684）与"正在安装守护进程…"文案（685 行）
- `handleExpressRepair` 与 `ExpressRepairService` 的前端调用
- Go 侧 `ExpressRepairService` 绑定方法 → 重新 `wails generate module`

**新增/改造**：

- `ExpressState` 增加 `daemonNeedsSetup bool`（巡检不健康派生，驱动"待准备"提示条）
  与 `daemonPreparing bool`（BusyCommand=="daemon-setup" 派生）
- 极速模式面板：
  - `daemonNeedsSetup && !daemonPreparing` → 提示条"极速模式需要安装网络组件。[立即准备]"
  - `daemonPreparing` → 进度条"正在准备极速模式，首次约需 30 秒…"，创建/加入按钮禁用
  - 准备失败 → 错误条 + 通用[重试]
- 准备失败文案映射（`expressPublicError` 增补通俗化分支）：
  - MSI 缺失 → "极速模式组件不完整，请重新安装 SoGame"
  - UAC 取消 → "准备已取消。重试需要管理员授权一次"
  - 杀软/执行失败 → "极速模式准备被系统阻止，请检查安全软件后重试"
- `service` 字段保留在状态中（诊断用），但前端不再渲染"守护进程"字样

### 4.6 安装包预装（installer/sogame.iss）

```pascal
[Run]
; 安装完成后静默安装 NetBird daemon（复用 helper 的四重校验链）。
; 失败不阻断 SoGame 安装完成：运行时 EnsureDaemonReady 会兜底补齐。
Filename: "{app}\sogame-helper.exe"; \
  Parameters: "--action install --artifact ""{app}\netbird_installer_0.74.7_windows_amd64.msi"" --log ""{commonappdata}\SoGame\netbird-install.log"" --result ""{commonappdata}\SoGame\netbird-install.result"""; \
  Flags: runhidden waituntilterminated; \
  StatusMsg: "正在安装网络组件（NetBird）…"
```

注意点：

- helper 的 `--log/--result` 白名单已含 `%ProgramData%\SoGame`，合规。
- helper 参数签名为 `--action, --artifact, --log, --result`（`--artifact` 指向
  MSI 路径；实现早期草稿曾用 `--msi`，落地时已更名——按本文档旧版维护会传错参数）。
- `waituntilterminated` 保证安装完成时 daemon 已就绪（msiexec 首装 ~15-30s，可接受）。
- 版本升级安装（覆盖装）时 MSI 同版，helper 内 `msiexec /i` 对已装同版产品是 no-op/快速返回，无感。

### 4.7 MSI 获取链加固（解决 R1）

- **分发**：发布 zip 与安装包已含 MSI（现状保持）；`publish-update.ps1` 缺 MSI 时从 warn 升级为 **fail**（防止再次发出缺件包）。
- **开发/自建**：`build-all.ps1` MSI 下载失败改为**硬失败**（当前 throw 但产物已生成，容易误发缺件目录）；支持 `-MsiPath` 参数手动指定本地 MSI。
- **运行时**：`windowsRepairFunc` 在 MSI 缺失时先查缓存位置（`%ProgramData%\SoGame\`、安装目录、SoGame.exe 同目录，三处依次查找），全部缺失才报"组件不完整"（文案引导重装）。

---

## 5. 兼容性

| 场景 | 行为 |
|---|---|
| 新用户（安装包） | postinstall 静默装好，零感知 |
| 老用户升级（已装 NetBird 0.74.7） | postinstall 同版 no-op；运行时巡检 Ready，无变化 |
| 老用户（曾点过"修复"按钮） | 升级后按钮消失；daemon 健康即无感，不健康看到"待准备"提示条，一次点击+UAC 补齐 |
| 绿色版/build\bin\ 运行（已确认保留） | 启动预检发现缺失 → 提示条 → 用户点击（或首次执行房间命令）触发 UAC 安装 |
| NetBird 被用户/杀软卸载 | 下次预检/命令时提示条或自动补齐（冷却+3 次上限防轰炸） |
| 未来 NetBird 版本漂移（0.74.8+，已确认策略 C） | 锁内置版本：`VersionMismatch` → 自动 REINSTALL 回内置版；**每次 SoGame 发版时评估升级 `netbird-release.json` 内置版本**（升级需重测 gRPC 契约，流程见 §7-T6/DEPLOY.md） |

## 6. 风险与缓解

| 风险 | 缓解 |
|---|---|
| postinstall 失败被杀软拦截，用户不知情 | 失败不阻断安装；首次使用走运行时兜底；安装日志落 `%ProgramData%\SoGame\` 供排查 |
| 首用时 UAC 弹窗打断体验 | 文案提前提示"首次使用需一次管理员授权"；后续使用无弹窗 |
| 杀软反复拦截导致 UAC 循环 | 60s 冷却 + 连续 3 次失败后停止自动重试 |
| 自动修复与房间命令并发 | 统一 BusyCommand 互斥（daemon-setup 视为一种命令） |
| 绿色版用户误以为"免安装"却看到 UAC | 首次准备进度文案说明"需要安装系统网络组件" |
| EnsureDaemonReady 等待 90s 阻塞 UI | Wails 绑定异步调用，前端 loading 态；超时后 Failed 可重试 |

## 7. 实施任务拆分（建议顺序）

1. **T1 `EnsureDaemonReady` + 启动预检**：`express_windows.go` 实现自动就绪（巡检→冷却→windowsRepairFunc→重组装），含 3 次上限与互斥；Startup 路径填 `daemonNeedsSetup`；单测（fake inspector + fake repair：成功/失败冷却/3 次上限/busy 等待）。
2. **T2 命令接入**：Create/Join/Reconnect 入口接 EnsureDaemonReady；`ExpressRepairService` 绑定删除；单测更新。
3. **T3 前端**：删除守护进程区块与 handleExpressRepair；"待准备"提示条 + daemonPreparing 进度提示 + 失败文案；`wails generate module` 重生成。
4. **T4 安装包**：sogame.iss `[Run]` postinstall；`publish-update.ps1` MSI 缺失改 fail + `-MsiPath`；`build-all.ps1` MSI 缺失改 fail + `-MsiPath`。
5. **T5 MSI 查找兜底**：windowsRepairFunc 三处查找（同目录 → 安装目录 → %ProgramData%\SoGame\）。
6. **T6 文案与文档**：expressPublicError 通俗化分支；AGENTS.md §3/§7 更新（去按钮化模型）；DEPLOY.md 发版清单加"评估升级 netbird-release.json 内置版本并重测 gRPC 契约"；README 用户向说明。

估计工作量：T1+T2 约 150 行核心改动，T3 约 60 行前端，T4/T5/T6 小改。总计 1 个工作日量级。

## 8. 测试计划

| 层 | 用例 |
|---|---|
| 单测 | EnsureDaemonReady：ready 直通/缺失自动修/修复失败冷却/3 次上限/与房间命令互斥/等待复用；chooseMSIAction 各健康态 |
| 集成（本机） | 卸载 NetBird → 启动 SoGame → 进极速模式：自动 UAC→安装→就绪→可建房（验证无按钮路径） |
| 安装包 | 干净 VM 安装：无 UAC、装完即可用极速模式 |
| 回归 | 既有 RepairService 测试改造为 EnsureDaemonReady 测试；B-1 死锁回归保留（自动路径同代码） |

## 9. 决策记录（2026-11-17 已确认）

1. **UAC 弹窗时机 → C. 应用启动时后台预检**：启动静默巡检，不健康先展示"待准备"
   提示条；UAC 仅在①用户点"立即准备"②用户直接执行房间命令时弹出。
2. **绿色版 → A. 保留**：运行时自动就绪作为正式路径维护。
3. **版本策略 → C. 每版 SoGame 评估升级内置版本**：运行时锁内置版本（不匹配自动
   REINSTALL 回内置版）；发版流程新增"评估升级 netbird-release.json 并重测 gRPC
   契约"步骤（写入 DEPLOY.md 发版清单）。
