# 第 6 阶段：squash 合入节点测速

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`60a697c feat: 添加连接成功详情弹窗`

## 合并方式

使用 squash 合入节点测速分支，再过滤旧基底、产物和不相关冲突。

```powershell
git merge --squash origin/feature/node-latency
```

## 合入来源

- `origin/feature/node-latency`
- 主要内容来自节点测速系列提交，包括异步测速、ICMP ping、GBK ping 输出解析、固定节点 chip 尺寸和手动测速按钮。

## 合入内容

- `internal/n2n/edge.go`：新增 `KnownNode`、`GetKnownNodes()`、`NodeLatencyInfo`、`MeasureNodeLatency()`、`MeasureAllNodesLatency()`。
- `internal/n2n/edge.go`：测速使用 Windows `ping`，字节级解析英文 `time=` 和中文 GBK `时间=` 输出。
- `internal/webui/app.go`：新增 `NodeLatencyInfo` 和 `GetNodesWithLatency()` Wails API。
- `internal/webui/app.go`：初次返回 `Latency: -2` 让 UI 立即显示节点，再后台异步测速并通过 `nodeLatencyUpdated` 事件推送结果。
- `frontend/src/App.jsx`：节点列表改为 `GetNodesWithLatency()`，监听 `nodeLatencyUpdated`，增加手动“测速”按钮和延迟展示。
- `frontend/src/index.css`：新增测速按钮、节点延迟、不可用节点等样式。
- `frontend/wailsjs/go/app/*` 和 `frontend/wailsjs/go/models.ts`：同步 Wails 绑定。

## 冲突与取舍

- `frontend/package-lock.json`、`installer/sogame.iss`、`internal/config/config.go`、`internal/n2n/edge.go`、`internal/webui/app.go`、`frontend/src/App.jsx` 均出现冲突。
- 已恢复 `.trae`、`frontend/dist/*`、`frontend/node_modules/*`、`frontend/package-lock.json`、`frontend/package.json.md5` 等旧基底/产物变化。
- 已恢复 `installer/sogame.iss`，避免旧分支覆盖安装器运行时 TAP 安装策略。
- 已恢复 `internal/config/config.go`，避免旧分支覆盖当前配置。
- `internal/n2n/edge.go` 先恢复当前 HEAD，再手工加入测速相关类型和函数，避免旧分支回退 edge 管理端口退出逻辑。
- `frontend/src/App.jsx` 手工合并，保留上一阶段连接详情弹窗，同时加入节点测速 UI。

## 管理端口保护点

已确认 `internal/n2n/edge.go` 仍保留：

- `mgmtPort` 字段。
- `BuildArgs(cfg, mgmtPort)` 与 `-t <port>`。
- `sendMgmtStop()`。
- `terminateEdgeProcess(..., mgmtPort)`。

## 验证

已执行：

```powershell
git grep -n -E '<<<<<<<|>>>>>>>' -- frontend/src internal frontend/wailsjs installer
go test ./internal/n2n ./internal/webui
npm --prefix frontend run build
git grep -n -E 'mgmtPort|sendMgmtStop|"-t"' -- internal/n2n/edge.go
```

结果：

```text
?    sogame/internal/n2n    [no test files]
?    sogame/internal/webui  [no test files]
frontend build passed
```

构建后已恢复 `frontend/dist/*` 产物变化。

## 提交前暂存文件

```text
frontend/src/App.jsx
frontend/src/index.css
frontend/wailsjs/go/app/App.d.ts
frontend/wailsjs/go/app/App.js
frontend/wailsjs/go/models.ts
internal/n2n/edge.go
internal/webui/app.go
```
