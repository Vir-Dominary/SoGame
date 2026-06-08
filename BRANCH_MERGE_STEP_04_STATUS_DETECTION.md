# 第 4 阶段：squash 合入连接状态修复

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`3586b68 chore: release v1.3.0, update branding and supernode address`

## 合并方式

先尝试 squash 合入状态修复分支，再收敛为最小源码改动。

```powershell
git merge --squash origin/fix/status-detection
```

由于该分支基于旧 develop，包含大量无关删除、产物和旧 edge 退出逻辑，最终恢复无关文件后手工移植状态修复。

## 合入来源

- `origin/fix/status-detection`
- 主要参考提交：`3be4b8f fix: 修复连接状态检测，统一状态机并输出转换日志`
- 主要参考提交：`173ac24 fix: 增加edge输出日志和TAP配置推断状态检测`

## 合入内容

- `internal/n2n/edge.go`：stdout 输出升为 info 日志，并继续进入状态解析。
- `internal/n2n/edge.go`：stderr 输出也进入 `parseEdgeOutput`，避免 edge 关键状态只出现在 stderr 时 UI 无法更新。
- `internal/n2n/edge.go`：扩展注册成功识别，支持 `<<< >>> supernode`、`edge_operate`、`supernode0: ok` 等输出。
- `internal/n2n/edge.go`：已注册状态不被后续中间状态降级。
- `internal/n2n/edge.go`：错误日志只在尚未连接/注册时转为错误状态，已连接后的非关键错误仅记录 debug。
- `internal/n2n/edge.go`：TAP 配置成功时推断为已注册，避免 UI 长时间停留在连接中。
- `internal/webui/app.go`：`StateConnected` 仅视为中间态，前端继续显示连接中；只有 `StateRegistered` 才进入已连接。

## 冲突与取舍

- `git merge --squash` 与 `frontend/package-lock.json`、`installer/sogame.iss`、`internal/config/config.go` 冲突。
- 已恢复 `frontend/dist/*`、`frontend/node_modules/*`、`frontend/package-lock.json`、`frontend/package.json.md5` 等产物/缓存变化。
- 已恢复 `.trae/rules/git-commit-message.md`，不把规则文件混入本阶段。
- 已恢复 `installer/sogame.iss`，避免旧分支重新引入安装器 TAP 运行步骤或覆盖第 3 阶段策略。
- 已恢复 `internal/config/config.go`，避免旧分支覆盖当前 release/branding 配置。
- 已恢复 `internal/n2n/edge.go` 后手工移植状态修复，避免旧分支删除 `mgmtPort` 和管理端口优雅退出逻辑。

## 管理端口保护点

已确认 `internal/n2n/edge.go` 仍保留：

- `mgmtPort` 字段。
- `BuildArgs(cfg, mgmtPort)` 与 `-t <port>`。
- `allocateUDPPort()`。
- `sendMgmtStop()`。
- `terminateEdgeProcess(..., mgmtPort)`。
- `runTaskkill(pid, force)`。

## 验证

已执行：

```powershell
git grep -n -E 'mgmtPort|sendMgmtStop|terminateEdgeProcess|runTaskkill|"-t"' -- internal/n2n/edge.go
git grep -n "GetKnownNodes" -- internal
go test ./internal/n2n ./internal/webui
```

结果：

```text
?    sogame/internal/n2n    [no test files]
?    sogame/internal/webui  [no test files]
```

## 提交前暂存文件

```text
internal/n2n/edge.go
internal/webui/app.go
```
