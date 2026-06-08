# 第 7.8 阶段：重启等待按 GUID 跟踪网卡

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`0ea5823 docs: 记录依赖检查拆分合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP 重启等待修复提交。

```powershell
git cherry-pick a024a4f
```

## 合入提交

- `a024a4f fix(tap): 重启等待按 GUID 跟踪网卡`

## 合入内容

- `internal/nic/wait.go`：增强等待逻辑，支持按稳定标识追踪网卡状态。
- `internal/nic/win_device.go`：设备层启停后等待目标网卡状态。
- `internal/tap/device.go`：TAP 重启按 GUID/NetCfg 身份跟踪，避免名称变化导致误判。
- 更新相关测试。

## 冲突与取舍

- cherry-pick 自动合并成功，无文本冲突。
- 未发现旧 `netjoin/internal` 导入路径。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/nic/... ./internal/tap
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
ok   sogame/internal/nic  30.987s
ok   sogame/internal/tap  6.278s
```

## 提交文件

```text
internal/nic/wait.go
internal/nic/win_device.go
internal/nic/win_device_test.go
internal/tap/device.go
internal/tap/device_test.go
```
