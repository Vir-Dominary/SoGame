# 第 7.10 阶段：添加 TAP 真实环境测试命令

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`4f2a9c2 docs: 记录 TAP ensure 根因保留合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 taptest 命令提交。

```powershell
git cherry-pick 9d1800c
```

## 合入提交

- `9d1800c chore(tap): 添加 TAP 真实环境测试命令`

## 合入内容

- 新增 `cmd/taptest/main.go`，用于真实 Windows 环境验证 TAP 查询、认领、创建、重启等流程。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 新增命令带有旧模块路径 `netjoin/internal/...`，本阶段已修正为 `sogame/internal/...`。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./cmd/taptest ./internal/tap ./internal/platform ./internal/nic/...
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
?    sogame/cmd/taptest       [no test files]
ok   sogame/internal/tap      (cached)
?    sogame/internal/platform [no test files]
ok   sogame/internal/nic      (cached)
```

## 提交文件

```text
cmd/taptest/main.go
```
