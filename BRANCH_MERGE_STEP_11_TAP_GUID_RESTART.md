# 第 7.5 阶段：按 GUID 重启并查找 TAP

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`e518478 fix(tap): 修正 TAP 创建阶段导入与查找策略`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP GUID 重启主题提交。

```powershell
git cherry-pick d167e43
```

## 合入提交

- `d167e43 refactor(tap): 按 GUID 重启并查找 TAP`

## 合入内容

- 新增 `internal/tap/device.go`，按名称/设备信息启用、重启 TAP 适配器。
- `internal/platform/tap.go`：`EnsureSoGameAdapter()` 在找到已知 GUID 或同名适配器后使用重启流程。
- `internal/platform/tap.go`：`FindTapInterfaceName()` 改为通过已知 GUID 查找当前接口名。
- `internal/n2n/edge.go` 和 `internal/webui/app.go` 接入 GUID 查找/重启后的 TAP 名称路径。

## 冲突与取舍

- `internal/platform/tap.go` 出现内容冲突，冲突点在 `FindTapInterfaceName()`。
- 冲突解决时选择严格 GUID 查找版本；不再 fallback 使用外部 TAP。
- 新增 `internal/tap/device.go` 和测试带有旧模块路径，已修为 `sogame/internal/nic`。
- `internal/n2n/edge.go` 合入时确认保留 `mgmtPort`、`-t <port>` 和 `sendMgmtStop()` 等管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/tap ./internal/platform ./internal/nic/... ./internal/n2n ./internal/webui
git grep -n "netjoin/internal\|FindFallbackInterfaceName" -- '*.go'
git grep -n -E 'mgmtPort|sendMgmtStop|"-t"' -- internal/n2n/edge.go
```

结果：

```text
ok   sogame/internal/tap       30.067s
?    sogame/internal/platform  [no test files]
ok   sogame/internal/nic       (cached)
?    sogame/internal/n2n       [no test files]
?    sogame/internal/webui     [no test files]
```

## 提交文件

```text
internal/n2n/edge.go
internal/platform/tap.go
internal/tap/device.go
internal/tap/device_test.go
internal/webui/app.go
```
