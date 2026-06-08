# 第 7.6 阶段：拆分 TAP IP 配置

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`733d925 docs: 记录 TAP GUID 重启合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 IP 配置拆分提交。

```powershell
git cherry-pick 31aa783
```

## 合入提交

- `31aa783 refactor(ipconfig): 拆分 TAP IP 配置`

## 合入内容

- 新增 `internal/ipconfig/tap.go`，集中处理 TAP IP、MTU、metric 配置。
- `internal/platform/tap.go` 改为调用 `internal/ipconfig`，继续作为 platform facade。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 新增 `internal/ipconfig/tap.go` 带有旧模块路径 `netjoin/internal/logger` 和 `netjoin/internal/tap`，本阶段已修正为 `sogame/internal/...`。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/ipconfig ./internal/platform ./internal/tap ./internal/nic/...
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
?    sogame/internal/ipconfig  [no test files]
?    sogame/internal/platform  [no test files]
ok   sogame/internal/tap       (cached)
ok   sogame/internal/nic       (cached)
```

## 提交文件

```text
internal/ipconfig/tap.go
internal/platform/tap.go
```
