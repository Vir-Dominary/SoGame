# 极速模式：嵌入 netbird 能力到 SoGame

## Context（背景）

SoGame 的 `feature/wireguard` 分支原本有一套**自研的** WireGuard P2P 控制平面（`wireguard/server` REST+WS+SQLite 控制服务器、`wireguard/agent` 本地 `sogame-agent.exe` 子进程 + STUN 探测 + `wg.exe` 接口管理）。实践证明这套方案 NAT 穿透与稳定性"收效甚微"，未达预期，**已于 2026-08 全部删除**。

当前方案：不 fork netbird，而是编排**官方 netbird v0.74.7 守护进程**（Windows 服务，本地 gRPC 41731 控制）+ 一个**服务端 Room API**（用 PAT 调 netbird Management REST，为每个房间创建 Group + 可复用 SetupKey + 同组放行 Policy）。客户端用 SetupKey **无头注册**，**无需登录/OAuth**。

**服务器现状（重要）**：
- `https://legengen.top` **已弃用**（不再使用，域名已不提供服务）。
- 当前使用**已部署的远程服务器**（形态：远程生产服务器；具体域名/地址暂未在文档与代码中补全，代码默认值 `DefaultRoomAPIURL` 仍为 `https://legengen.top`，**待更新为新的 Room API 地址**，否则新装机无法创建/加入房间）。
- 房间码/注册链路完全依赖该服务器可达。

**用户已确认的四项决策**：
1. 控制平面：最终把 Room API 移植进 SoGame（本次服务端后做，客户端先复用已部署服务器）。
2. 客户端守护进程：打包官方 netbird MSI，首装时 UAC 安装为 Windows 服务。
3. 旧的自研控制平面：**已删除**，由 netbird 方案替换。
4. 本次范围：**客户端优先**，服务端后做。

## 架构（现状）

```
SoGame.exe (Wails, internal/webui.App)
  ├─ 经典模式：n2n + tap-windows（保持不变）
  └─ 极速模式：netbird 编排
       ├─ internal/netbird/      本地 gRPC 适配器 → 官方 netbird 守护进程 (127.0.0.1:41731)
       │                           Login(SetupKey)/Up/Down/Logout/Status/SubscribeEvents
       │                           管理 "sogame-room" 单一 profile
       ├─ internal/roomapi/      HTTP 客户端 → Room API（已部署的远程服务器，地址待补全）
       │                           POST /rooms (创建) / POST /rooms/join (加入) / GET /rooms/{code}/peers
       ├─ internal/session/      单房间状态机：NoRoom→Enrolling→WaitingForPeer→ConnectingPeer→ConnectedP2P/Relay
       ├─ internal/securestore/  DPAPI 保护 room code + metadata (LOCALAPPDATA\Sogame\NetBird\)
       └─ internal/nbdaemon/     官方 netbird MSI 安装/服务查询/修复 (UAC)
```

数据面完全由官方 netbird 守护进程负责（WireGuard + ICE/STUN + Relay 回退），SoGame 不实现任何网络协议。房间码即"邀请码"：创建房间返回 room code，加入方凭 room code 换 SetupKey 无头注册。

## 实施步骤与状态

### 0. 分支与前置 — ✅ 完成
当前工作分支已含全部 netbird 集成改动，模块名 `sogame`。

### 1. 删除自研控制平面 — ✅ 完成
已删除：
- `wireguard/agent/`、`wireguard/server/`、`wireguard/web/`、`wireguard/deploy/`（整目录）
- `cmd/taptest/`、`scripts/start-wg-test.ps1`、`scripts/test-wg-admin.ps1`
- `internal/webui/wgservers.go`、`internal/webui/process_other.go`、`internal/webui/process_windows.go`

### 2. 移植客户端核心包到 `sogame/internal/` — ✅ 完成
`internal/netbird/`（gRPC 适配器 + `rpc/` 生成契约）、`internal/roomapi/`、`internal/session/`、`internal/securestore/`、`internal/nbdaemon/`、`internal/observability/redact.go` 全部就位，import 路径已改写，无 `legengen/sogame-netbird` 残留。

### 3. go.mod 依赖 — ✅ 完成
grpc/protobuf 引入，`go test ./internal/...` 持续全绿。

### 4. 配置层 `internal/config/config.go` — ✅ 完成（含待办）
- `RoomAPIURL string`（**默认值仍为 `https://legengen.top`，待替换为新服务器地址**）
- `ExpressNickname string`（极速模式显示名）
- 旧 `WGServerURL`/`WGInviteCode` 字段已移除
- 房间码不进 config，由 `securestore` DPAPI 保护

### 5. 重写 `internal/webui/app.go` 极速模式部分 — ✅ 完成
- 旧 sogame-agent 子进程逻辑、`WGCreateRoom`/`WGJoinRoom`/`WGDisconnect`/`WGGetStatus`/`WGGetInviteCode`、`GetWGServers`/`SaveWGSettings` 旧签名全部移除。
- 新增 `internal/webui/express.go`：`ExpressController`（`Configure`/`Startup`/`GetState`/`CreateRoom`/`JoinRoom`/`Reconnect`/`Disconnect`/`LeaveRoom`/`RevealRoomCode`/`RepairService`）+ `refreshRoomView`（5s）/`refreshService` 轮询 + `express:state-changed` 事件推送。
- `ExpressState` 现含 `roomCode`（明文房间码随轮询快照下发，见"已知问题"）、`disconnected`（断开态）、`hasSavedRoom`（恢复弹窗）等字段。
- 经典模式逻辑未动。

### 6. 前端 `frontend/src/App.jsx` 极速模式重写 — ✅ 完成
- 创建/加入表单、房间成员列表（`peers` 渲染）、连接状态文案/颜色映射、房间码卡片 + 复制按钮、断开/离开按钮、断开后"重新连接"按钮（`ControlPlaneConnected` 态）、服务修复按钮、恢复上次房间弹窗。
- `frontend/wailsjs/go/app/App.js`/`App.d.ts`/`models.ts` 已重新生成。

### 7. 安装器 `installer/sogame.iss` — ⏳ 待完成
- 旧 `wireguard.exe`/`wg.exe`/`sogame-agent.exe` 文件项已移除。
- **待办**：放入官方 netbird v0.74.7 MSI 到 `installer/netbird/` 并加 `[Files]`/`[Run]` 项；在干净 Windows 10/11 验证安装、`sc query NetBird` 服务存在。

### 8. 构建脚本清理 — ✅ 完成
`scripts/build-all.ps1` 已移除 sogame-agent 构建步骤；`process_*.go` 已删除。

## 验证状态

| 验证项 | 状态 | 说明 |
|---|---|---|
| `go build ./...` | ✅ | 持续通过 |
| `go test ./internal/...` | ✅ | session/webui/roomapi/netbird 等全绿 |
| 前端构建（wails build） | ✅ | 通过 |
| 单机创建→入网→房间界面 | ✅ | 通过（Room API 指向已部署远程服务器时） |
| 房间码展示/复制 | ✅ | 已修复（见"已知问题"） |
| 断开→重新连接按钮 | ✅ | 已实现（`ControlPlaneConnected` 态显示"重新连接"） |
| 双机联机（创建端 + 加入端互通） | ⏳ | **待验证** |
| 干净环境安装（netbird MSI 自动安装） | ⏳ | 依赖步骤 7 |

## 已知问题与优化点（2026-08 记录）

1. **服务器地址未补全（高优先级）**：`internal/config/app_config.go` 的 `DefaultRoomAPIURL` 仍为 `https://legengen.top`（已弃用）。需替换为当前已部署服务器的实际地址，并同步更新本仓库内 mock/测试中的 URL（`tools/room-api-mock/main.go`、`internal/roomapi/*_test.go` 中的 `https://legengen.top`）。
2. **房间码 reveal 链路（已修复）**：Wails 对 `(string, *ExpressError)` 绑定返回的 Promise 在前端 `.then` 中不可用（`expressRoomCodeRevealed` 从未被设置为非空，导致前端每 1-3 秒重试一次）。**已改为** `ExpressState.RoomCode` 随 3 秒轮询快照直接下发，前端渲染与复制均读取 `expressState.roomCode`；`ExpressRevealRoomCode` 保留作兜底。
3. **断线自动检测**：netbird 守护进程状态异常时前端显示"重新连接"依赖 `ControlPlaneConnected` 态判断；daemon 意外退出/重启场景的 UI 提示仍可优化（如显示 daemon 异常原因）。
4. **成员列表"在线/离线"准确性**：`peers` 的 `connected` 来自 daemon 对等端状态，断线后可能存在短暂延迟，可加"最近活跃时间"字段。

## 后续（本次不做）
- Room API 服务端移植进 SoGame（自包含控制平面，摆脱对外部服务器的依赖）。
- 诊断打包（`diagnostics`）、系统托盘、自动更新。
- 多房间 / 房间管理后台。
- 服务器选择/多节点容灾（Room API 多实例 + 健康探测）。
