# 第 7.4 阶段：按 GUID 策略认领和创建 SoGame TAP

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`111ee01 fix(tap): 修正持久身份导入路径`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP 认领/创建主题提交。

```powershell
git cherry-pick 72dba80
```

## 合入提交

- `72dba80 refactor(platform): 按 GUID 策略认领和创建 SoGame TAP`

## 合入内容

- `internal/tap/create.go`：新增 TAP 安装前后快照比较，识别本次新建的 TAP 适配器。
- `internal/tap/create.go`：新增按 LUID 重命名新 TAP 适配器为 `SoGame-VPN`。
- `internal/platform/tap.go`：`EnsureSoGameAdapter()` 改为按已知 GUID 解析、同名检查、创建新实例的流程。
- 新增创建逻辑测试。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 新增文件带有旧模块路径 `netjoin/internal/nic`，本阶段已修为 `sogame/internal/nic`。
- `internal/platform/tap.go` 仍引用已移除的 `FindFallbackInterfaceName()`，本阶段改为只通过 `ResolveKnownAdapter()` 和同名 `SoGame-VPN` 查找 TAP，不再 fallback 到外部 TAP。
- 该处理符合“只操作 SoGame 认领或本次新建 TAP，不盲用外部 TAP”的策略。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/tap ./internal/platform ./internal/nic/...
git grep -n "FindFallbackInterfaceName\|netjoin/internal" -- '*.go'
```

结果：

```text
ok   sogame/internal/tap       (cached)
?    sogame/internal/platform  [no test files]
ok   sogame/internal/nic       (cached)
```

## 提交文件

```text
internal/platform/tap.go
internal/tap/adapter.go
internal/tap/create.go
internal/tap/create_test.go
```
