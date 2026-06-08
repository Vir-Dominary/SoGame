# 第 1 阶段：合并 nic 基础能力

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 基底：`origin/main`
- 起点提交：`181800f Merge pull request “fix: edge 优雅退出，解决重连时 supernode 认证冲突” #2 from Vir-Dominary/fix/stop-graceful-shutdown`
- 备份分支：`rebase-test-backup-before-rebuild`

## 合并方式

使用 `cherry-pick -n` 依次应用四个 nic 相关提交，最后合并为一个主题提交。

```powershell
git cherry-pick -n 62d03db
git cherry-pick -n 36dc99c
git cherry-pick -n 1858108
git cherry-pick -n 0b563b0
git commit -m "feat(nic): 添加 Windows 网卡查询与设备控制能力"
```

## 合入提交

- `62d03db feat(nic): 新增 Windows 网卡查询基础能力`
- `36dc99c feat(nic): 支持通过 LUID 解析 NetCfg 标识`
- `1858108 feat(nic): 支持设备层启停网卡并等待状态`
- `0b563b0 chore(nic): 添加网卡设备层调试命令`

## 合入内容

- 新增 `internal/nic` 包，提供 Windows 网卡枚举、按 LUID/GUID/名称查询、NetCfgInstanceId 解析能力。
- 新增设备层启停和等待逻辑，用于后续 TAP GUID 所有权策略。
- 新增 `internal/poll` 通用等待工具。
- 新增 `cmd/nicctl` 调试命令，便于真实环境查看和操作网卡。

## 冲突处理

- 四个 `cherry-pick -n` 均无冲突。
- 本阶段未改动 `internal/n2n/edge.go`，因此未影响 `origin/main` 已包含的管理端口优雅退出逻辑。
- 当前 `origin/main` 的模块路径仍是 `netjoin`，本阶段保持基底一致，不提前修改为 `sogame`。模块路径会在后续 release/branding 阶段统一处理。

## 验证

已执行：

```powershell
go test ./internal/nic/... ./internal/poll/...
```

结果：

```text
ok   netjoin/internal/nic   5.496s
ok   netjoin/internal/poll  (cached)
```

## 提交前暂存文件

```text
cmd/nicctl/main.go
internal/nic/adapter.go
internal/nic/adapter_test.go
internal/nic/netcfg.go
internal/nic/netcfg_test.go
internal/nic/query.go
internal/nic/query_test.go
internal/nic/wait.go
internal/nic/wait_test.go
internal/nic/win_device.go
internal/nic/win_device_test.go
internal/poll/wait.go
internal/poll/wait_test.go
```
