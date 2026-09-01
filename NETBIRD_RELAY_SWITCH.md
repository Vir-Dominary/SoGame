# NETBIRD_RELAY_SWITCH.md — Relay 开关（SoGame 服务器侧实现，阶段 B）

> 位置：`C:\Gitclone\git\SoGame`（分支 `feature/wireguard`）
>
> **设计原则：Relay 开关由提供服务的服务器（Room API）掌握，随房间下发给客户端；客户端不掌握开关权限，只被动遵循。**

---

## 1. 架构与数据流

```text
┌───────────────────────────────────────────────────────────┐
│ Room API 服务器（SoGame server/room-api）                    │
│   配置：ROOM_API_RELAY_ENABLED=true|false（默认 false）       │
│   └─ 创建/加入房间响应携带 relay_enabled 字段                  │
└──────────────────────────┬────────────────────────────────┘
                           │ HTTPS（create/join）
                           ▼
┌───────────────────────────────────────────────────────────┐
│ SoGame 客户端                                               │
│   roomapi 客户端解析 relay_enabled                           │
│   └─ 持久化到房间元数据 room.json（relayEnabled）             │
│   └─ 状态机 Facts.RelayAllowed                               │
│        ├─ relay 不允许：中继连接不视为已连接（纯 P2P 优先）      │
│        └─ relay 允许：恢复原 ConnectedRelay 行为              │
│   └─ UI：relay 关闭时提示"该服务器已关闭中继，仅支持 P2P 直连"   │
└───────────────────────────────────────────────────────────┘
```

## 2. 配置方式（服务器侧）

### 公用服务器（默认：纯 P2P，不允许中继）

```bash
ROOM_API_RELAY_ENABLED=false   # 默认即 false，可省略
```

### 私有服务器（允许中继回退）

```bash
ROOM_API_RELAY_ENABLED=true
```

切换方式：**改服务器环境变量 → 重启 Room API → 新创建/加入的房间生效**。客户端无需任何配置。

## 3. 改动清单

| 文件 | 改动 |
|---|---|
| `server/room-api/internal/config/config.go` | 新增 `RelayEnabled`（env `ROOM_API_RELAY_ENABLED`，默认 false）+ `boolEnv` |
| `server/room-api/internal/rooms/service.go` | `RoomResponse` 新增 `relay_enabled`；Create/Join 随房间下发 |
| `server/room-api/cmd/room-api/main.go` | 注入 `RelayEnabled` |
| `internal/roomapi/client.go` | `enrollmentResponse`/`Enrollment` 新增 `RelayEnabled` 并解析 |
| `internal/securestore/metadata.go` | `RoomMetadata` 新增 `RelayEnabled`（旧数据默认 false） |
| `internal/session/state.go` | `Facts.RelayAllowed`；`preferredConnectedPath` 在不允许时忽略中继 |
| `internal/session/service.go` | enroll 时持久化 `RelayEnabled`；View 时注入 `Facts.RelayAllowed` |
| `internal/webui/express.go` | `ExpressState.RelayEnabled` / `RelayBlocked`；纯 P2P 下中继路径置 none |
| `frontend/src/App.jsx` / `index.css` | 房间内提示"该服务器已关闭中继" / "无法建立 P2P 直连" |
| `tools/room-api-mock/main.go` | mock 响应携带 `relay_enabled`（`MOCK_RELAY_ENABLED`） |

## 4. 生命周期覆盖清单（客户端行为）

### `relay_enabled=false`（服务器不允许中继）

| 检查项 | 状态 | 依据 |
|---|---|---|
| 客户端不把中继连接视为"已连接" | ✅ | `preferredConnectedPath` 忽略 `PathRelay` → 不进 `ConnectedRelay` |
| 状态机停留在"连接中/重连"而非"中继已连接" | ✅ | relay 被忽略 → `ConnectingPeer`/`Reconnecting` |
| UI 不显示"中继"标签 | ✅ | `express.go` 将中继路径映射为 none |
| UI 明确提示"该服务器已关闭中继" | ✅ | `relayEnabled=false` 常驻提示 |
| UI 明确提示"无法建立 P2P 直连" | ✅ | `relayBlocked`（底层检测到中继被忽略）时警告提示 |
| 不影响 P2P/STUN/Signal/Management | ✅ | 仅路径判定逻辑改变 |

### `relay_enabled=true`（服务器允许中继）

| 检查项 | 状态 |
|---|---|
| 中继连接恢复为"已连接（中继）" | ✅ `ConnectedRelay` 恢复 |
| UI 恢复"中继"标签 | ✅ |
| P2P 优先逻辑不变 | ✅ `preferredConnectedPath` 仍 P2P 优先 |

## 5. 测试结果

```text
go build ./internal/... ./cmd/... ./tools/room-api-mock/...        ✅
go test  ./internal/session/  ./internal/roomapi/  ./internal/securestore/  ./internal/webui/  ✅
go build ./...（server/room-api 独立 module）                       ✅
go test  ./...（server/room-api 独立 module）                       ✅
```

新增/更新测试：

- `internal/session/path_test.go`：新增 `TestFactsFromDaemonPureP2PIgnoresRelay`（服务器不允许中继时，中继连接不被视为已连接）
- `internal/session/state_test.go`：新增"relay ignored when server disallows"用例；relay 用例显式 `RelayAllowed: true`
- `internal/session/scenario_test.go`：relay fallback 用例显式 `RelayAllowed: true`

## 6. 部署验证（用户环境）

```bash
# 公用服务器（纯 P2P）
ROOM_API_RELAY_ENABLED=false  # 默认

# 私有服务器（允许中继）
ROOM_API_RELAY_ENABLED=true

# 重启 Room API 后，创建新房间 → 客户端读取 relay_enabled
# 验证：
#   false：双机 NAT 打洞失败时，客户端显示"无法建立 P2P 直连"，不显示"中继"
#   true ：P2P 失败可回退中继，显示"已连接 · 中继"
```
