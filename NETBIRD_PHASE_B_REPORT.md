# NETBIRD_PHASE_B_REPORT.md — 阶段 B 执行报告（SoGame 服务器侧实现）

> 任务：B. Relay 开关 + 裁剪 A 类模块
> 最终实现位置：`C:\Gitclone\git\SoGame`（分支 `feature/wireguard`）
> 关键原则：**Relay 开关由提供服务的服务器（Room API）掌握，客户端不掌握开关权限**

---

## 1. 实现方式更正说明

上一轮误在 NetBird fork（`C:\GitClone\Git\NetBird`）上实现（combined 内 `server.relay.enabled`）。经确认，**正确位置是 SoGame 项目**：SoGame 复用 NetBird 方法（daemon RPC 适配、状态机、Room API 控制面），Relay 开关应在**复用的方法里**由**服务器**（Room API）掌握。

**需要用户手动回退 fork 上的误改**（我无权限操作 fork）：

```bash
cd C:\GitClone\Git\NetBird
git checkout main
git branch -D feature/netbird-minimal-core
```

## 2. 修改清单（SoGame）

| 文件 | 改动 |
|---|---|
| `server/room-api/internal/config/config.go` | `ROOM_API_RELAY_ENABLED`（默认 false） |
| `server/room-api/internal/rooms/service.go` | `RoomResponse.relay_enabled`，Create/Join 下发 |
| `server/room-api/cmd/room-api/main.go` | 注入配置 |
| `internal/roomapi/client.go` | 解析 `relay_enabled` → `Enrollment.RelayEnabled` |
| `internal/securestore/metadata.go` | `RoomMetadata.RelayEnabled` 持久化 |
| `internal/session/state.go` | `Facts.RelayAllowed`；纯 P2P 忽略中继 |
| `internal/session/service.go` | enroll 持久化 + View 注入 |
| `internal/webui/express.go` | `relayEnabled`/`relayBlocked` 状态呈现 |
| `frontend/src/App.jsx`、`index.css` | 纯 P2P 提示与"无法直连"警告 |
| `tools/room-api-mock/main.go` | mock 携带 `relay_enabled` |

## 3. A 类模块处理

- **部署级（无需改代码）**：coturn、Dashboard 不部署（SoGame 用 combined 镜像内嵌 STUN、自有 UI）。
- **代码级（推迟）**：agent-network / upload-server / flow / proxy 深耦合或仅客户端使用，按"高复杂度 + 低收益 → 暂保留"原则推迟，详见 `NETBIRD_FEATURE_AUDIT.md`。
- **relay 接受层（本轮已做）**：客户端不再默认把中继连接视为已连接，由服务器 `relay_enabled` 控制——这是 SoGame 复用层里"裁剪 A 类（relay 依赖）"的实际落点。

## 4. 测试结果

```text
主 module：
  go build ./internal/... ./cmd/... ./tools/room-api-mock/...   ✅
  go test ./internal/session ./internal/roomapi ./internal/securestore ./internal/webui   ✅
room-api（独立 module）：
  go build ./...   ✅
  go test  ./...   ✅
```

## 5. 未解决问题

1. fork 回退需用户执行（`git checkout main && git branch -D feature/netbird-minimal-core`）。
2. 前端改动需重新构建 SoGame.exe / 安装包后生效（或由用户构建）。
3. 双机真机验证：`relay_enabled=false` 时 NAT 打洞失败应显示"无法建立 P2P 直连"而非"中继"；`true` 时恢复中继回退。
4. 服务器重启后旧房间（元数据已存 relayEnabled）行为一致；幂等缓存响应保留旧值（合理）。
