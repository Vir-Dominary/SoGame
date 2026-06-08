# 第 2 阶段：合入 develop 基础配置

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`7d2c9d1 feat(nic): 添加 Windows 网卡查询与设备控制能力`

## 合并方式

先无提交应用旧 develop 基础提交，再保护 `origin/main` 中已经合并的 edge 管理端口优雅退出逻辑。

```powershell
git cherry-pick -n 54e3d84
git restore --source=origin/main --staged --worktree internal/n2n/edge.go internal/webui/app.go frontend/dist/index.html frontend/node_modules/.package-lock.json frontend/package.json.md5
```

随后手工同步模块路径和必要编译修复。

## 合入提交

- `54e3d84 用于开发和测试的新版本。这是一个不稳定版本`

## 合入内容

- `go.mod` 模块名从 `netjoin` 切换为 `sogame`。
- 前端包名和基础 UI 内容同步到 develop 版本。
- 配置、平台依赖检查、网络配置、安全加密、应用入口等基础源码同步到 develop 版本。
- 第 1 阶段新增的 `cmd/nicctl` 和 `internal/nic/wait.go` 导入路径同步为 `sogame/internal/...`。

## 冲突与取舍

- Git 无文本冲突。
- `54e3d84` 会把 `internal/n2n/edge.go` 回退到旧的 `os.Interrupt + taskkill /F` 断开逻辑，因此本阶段恢复 `origin/main` 的 `edge.go` 语义，只保留模块路径改名。
- `internal/webui/app.go` 同样先恢复 `origin/main` 语义，只保留模块路径改名；连接详情、测速和 TAP ensure 后续按主题合入。
- 丢弃 `frontend/dist/index.html`、`frontend/node_modules/.package-lock.json`、`frontend/package.json.md5` 等产物/缓存变化。
- `go test` 在 Go 1.26 下暴露 `fmt.Errorf(a.errMsg)` 的 vet 错误，本阶段用 `errors.New(a.errMsg)` 做等价修复。

## 管理端口保护点

已确认 `internal/n2n/edge.go` 保留：

- `mgmtPort` 字段。
- `BuildArgs(cfg, mgmtPort)` 和 `-t <port>` 启动参数。
- `allocateUDPPort()`。
- `sendMgmtStop()`。
- `terminateEdgeProcess(..., mgmtPort)`。
- `runTaskkill(pid, force)`。

## 验证

已执行：

```powershell
git grep -n "netjoin/internal" -- '*.go'
git grep -n -E 'mgmtPort|sendMgmtStop|terminateEdgeProcess|runTaskkill|"-t"' -- internal/n2n/edge.go
go test ./internal/nic/... ./internal/poll/... ./internal/n2n ./internal/webui
```

结果：

```text
ok   sogame/internal/nic    (cached)
ok   sogame/internal/poll   (cached)
?    sogame/internal/n2n    [no test files]
?    sogame/internal/webui  [no test files]
```

## 提交前暂存文件

```text
cmd/nicctl/main.go
frontend/package-lock.json
frontend/package.json
frontend/src/App.jsx
go.mod
internal/config/config.go
internal/n2n/edge.go
internal/nic/wait.go
internal/platform/dependencies.go
internal/platform/network.go
internal/platform/tap.go
internal/security/encryption.go
internal/webui/app.go
main.go
```
