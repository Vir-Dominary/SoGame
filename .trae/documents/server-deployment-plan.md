# 服务器部署方案（旧自研 WG 控制服务器 — 已废弃）

## 状态总览

> **本方案已废弃**。文档描述的 `wireguard/server`（自研 WG 控制服务器 REST+WS+SQLite）、`wireguard/web`（管理面板）、`wireguard/deploy`（Docker 部署）等**全部已从仓库删除**（2026-08），被"官方 netbird 守护进程 + Room API"方案取代（见 `express-mode-netbird-integration.md`）。

## Context（原背景）

原计划：部署极速模式的 WireGuard 控制服务器到生产环境，配套 Web 管理面板、Docker 编排与裸机 systemd 方案。存在以下问题（均已随废弃解决）：
1. Admin API 无认证
2. docker-compose 环境变量硬编码
3. HTTPS 切换不便
4. 无部署文档 / 无 systemd 方案

## 当前服务器的实际情况（2026-08 更新）

- `legengen.top`（曾作为 Room API / Management 地址）**已弃用，不再使用**。
- 当前极速模式连接的是**已部署的远程 Room API 服务器**（形态：远程生产服务器）。
- **服务器地址暂未在文档/代码中补全**：`internal/config/app_config.go` 的 `DefaultRoomAPIURL` 仍为 `https://legengen.top`（占位，待替换），`tools/room-api-mock/` 与 `internal/roomapi/*_test.go` 中的 mock 地址同样待同步更新。

## 旧改动清单（已删除，仅存档）

### 1. 服务端代码：Admin Token 认证（未实施 — 整体废弃）
### 2. Web 管理面板：适配 Admin Token（未实施 — 整体废弃）
### 3. Docker 部署配置完善（未实施 — 整体废弃）
### 4. 裸机部署方案（systemd）（未实施 — 整体废弃）
### 5. 部署脚本（未实施 — 整体废弃）
### 6. 部署文档（未实施 — 整体废弃）

## 现状替代方案（进行中）

| 项 | 状态 | 说明 |
|---|---|---|
| Room API 服务端（自研，可脱离外部服务器） | ⏳ 后续 | 移植进 SoGame，作为后续工作 |
| 客户端接入已部署远程服务器 | ✅ | 极速模式创建/加入走该服务器 |
| 服务器地址补全（替换 legengen.top） | ⏳ 待办 | 高优先级，地址确定后更新代码默认值与文档 |
| 多服务器容灾/健康探测 | ⏳ 后续 | 未规划实现 |

## 验证（原方案，未执行 — 整体废弃）

原计划的 Docker 构建验证、Admin 认证验证、Web 面板验证、Go 编译验证、systemd 验证均随方案废弃不再适用。
