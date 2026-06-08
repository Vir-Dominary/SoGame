# 第 7.7 阶段：拆分依赖检查

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`833d33f fix(ipconfig): 修正 TAP 配置导入路径`

## 合并方式

直接 cherry-pick 当前已整理好的依赖检查拆分提交。

```powershell
git cherry-pick 94f0270
```

## 合入提交

- `94f0270 refactor(diagnostics): 拆分依赖检查`

## 合入内容

- 新增 `internal/diagnostics/dependencies.go`，集中处理依赖检查。
- `internal/platform/dependencies.go` 改为 facade，转调 diagnostics 包。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 未发现旧 `netjoin/internal` 导入路径。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/diagnostics ./internal/platform
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
?    sogame/internal/diagnostics  [no test files]
?    sogame/internal/platform     [no test files]
```

## 提交文件

```text
internal/diagnostics/dependencies.go
internal/platform/dependencies.go
```
