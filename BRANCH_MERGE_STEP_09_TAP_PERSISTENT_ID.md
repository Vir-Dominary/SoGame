# 第 7.3 阶段：使用 NetCfgInstanceId 持久化 TAP 身份

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`147a7e5 docs: 记录 TAP 查询拆分合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP 持久身份主题提交。

```powershell
git cherry-pick b0adb9a
```

## 合入提交

- `b0adb9a refactor(tap): 使用 NetCfgInstanceId 持久化 TAP 身份`

## 合入内容

- 新增 `internal/tap/store.go`，持久化 TAP 身份信息。
- 新增 `internal/tap/remember.go`，记录已认领的 TAP 适配器。
- 新增 `internal/tap/resolve.go`，按持久化 `NetCfgInstanceId` 解析 TAP。
- 更新 platform facade 以使用 TAP 持久身份。
- 新增对应单元测试。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 该提交带有旧模块路径 `netjoin/internal/nic`，本阶段已直接修正为 `sogame/internal/nic`，避免后续补救提交。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/tap ./internal/platform ./internal/nic/...
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
ok   sogame/internal/tap       30.066s
?    sogame/internal/platform  [no test files]
ok   sogame/internal/nic       (cached)
```

## 提交文件

```text
internal/platform/tap.go
internal/tap/adapter.go
internal/tap/remember.go
internal/tap/remember_test.go
internal/tap/resolve.go
internal/tap/resolve_test.go
internal/tap/store.go
internal/tap/store_test.go
```
