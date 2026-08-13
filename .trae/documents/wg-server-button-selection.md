# 极速模式控制服务器按钮选择（旧方案 — 已废弃）

## 状态总览

> **本方案已废弃**。文档描述的"WG 控制服务器按钮选择"（预设列表 + 测速 + `GetWGServers`/`SaveWGSettings` + `wgServerLatencyUpdated` 事件 + `scripts/start-wg-test.ps1` 测试脚本）**全部已从仓库删除**（2026-08）：
> - `internal/webui/wgservers.go` 已删除
> - `scripts/start-wg-test.ps1`、`scripts/test-wg-admin.ps1` 已删除
> - `WGCreateRoom`/`WGJoinRoom`/`GetWGServers`/`SaveWGSettings` 等旧 Wails 绑定已移除

## 废弃原因

1. 自研 WG 控制平面（sogame-server / sogame-agent 子进程）整体被官方 netbird 守护进程方案取代（见 `express-mode-netbird-integration.md`）。
2. 极速模式不再需要用户选择/输入控制服务器——Room API 地址由配置固定（`RoomAPIURL`），客户端无头注册 netbird，无需任何服务器选择 UI。

## 旧方案内容（仅存档）

- 预设服务器列表（本地测试 / 官方占位 / 自定义输入）
- HTTP `/health` 并发测速 + `wgServerLatencyUpdated` 事件推送
- chip 按钮 UI（复用经典模式 `node-chips` 样式）
- `SaveWGSettings` 配置持久化
- `scripts/start-wg-test.ps1` 一键启动（编译 + UAC 提权 + 健康检查）

## 现状替代（极速模式服务器相关）

| 项 | 状态 | 说明 |
|---|---|---|
| Room API 服务器地址配置 | ✅ 代码已有 | `cfg.RoomAPIURL`（config），前端不再有服务器选择 UI |
| 服务器地址默认值 | ⏳ 待办 | `DefaultRoomAPIURL` 仍为 `https://legengen.top`（已弃用），待替换为已部署服务器地址 |
| 服务器健康状态可见性 | ⏳ 后续优化 | 当前无独立的服务器连通性提示；Room API 不可达时表现为创建/加入命令失败，可优化为明确的"服务器不可达"提示 |

## 验证（原方案，未执行 — 整体废弃）

原计划的"3 个按钮 + 测速 + 持久化 + 一键启动脚本"验证随方案废弃不再适用。
