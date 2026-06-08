# 第 7.9 阶段：阻断 TAP ensure 失败并保留根因

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`9375cf7 docs: 记录 TAP GUID 等待合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP ensure 修复提交。

```powershell
git cherry-pick 4debdb8
```

## 合入提交

- `4debdb8 fix(tap): 阻断 TAP ensure 失败并保留根因`

## 合入内容

- `internal/platform/tap.go`：TAP ensure 失败时返回失败状态和根因，不再吞掉错误继续连接。
- `internal/nic/netcfg.go`：增强 NetCfg 查询错误处理，便于保留根因。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 未发现旧 `netjoin/internal` 导入路径。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/nic/... ./internal/platform ./internal/tap
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
ok   sogame/internal/nic       7.173s
?    sogame/internal/platform  [no test files]
ok   sogame/internal/tap       30.079s
```

## 提交文件

```text
internal/nic/netcfg.go
internal/platform/tap.go
```
