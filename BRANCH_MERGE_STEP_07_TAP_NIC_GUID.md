# 第 7.1 阶段：TAP GUID 查询与友好名改名

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`143b153 feat: 增加节点延迟检测和展示`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP/nic 主题提交。

```powershell
git cherry-pick 1733eda
```

## 合入提交

- `1733eda feat(nic): 支持 TAP GUID 查询与友好名改名`

## 合入内容

- `internal/nic/netcfg.go`：扩展 NetCfg 查询能力，用于按 TAP/GUID 定位连接。
- `internal/nic/rename.go`：新增 Windows 连接友好名改名能力。
- `cmd/nicctl/main.go`：新增相关调试命令。
- 新增/更新 nic 单元测试。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 未触碰 `internal/n2n/edge.go`，因此不影响 edge 管理端口优雅退出逻辑。
- 导入路径保持 `sogame/internal/...`。

## 验证

已执行：

```powershell
go test ./internal/nic/...
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
ok   sogame/internal/nic   30.993s
```

## 提交文件

```text
cmd/nicctl/main.go
internal/nic/netcfg.go
internal/nic/netcfg_test.go
internal/nic/rename.go
internal/nic/rename_test.go
```
