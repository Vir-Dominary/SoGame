# 第 7.2 阶段：拆分 TAP 查询与资源定位逻辑

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`4ab7e0d docs: 记录 TAP GUID nic 合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 TAP 查询拆分主题提交。

```powershell
git cherry-pick 11b38bf
```

## 合入提交

- `11b38bf refactor(tap): 拆分 TAP 查询与资源定位逻辑`

## 合入内容

- 新增 `internal/tap/adapter.go`，承载 TAP 适配器查询和名称识别逻辑。
- 新增 `internal/tap/assets.go`，承载 TAP 驱动资源路径定位逻辑。
- `internal/platform/tap.go` 收敛为 platform facade，调用 `internal/tap` 提供的能力。

## 冲突与取舍

- cherry-pick 无文本冲突。
- 未触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。
- 导入路径保持 `sogame/internal/...`。

## 验证

已执行：

```powershell
go test ./internal/tap ./internal/platform
git grep -n "netjoin/internal" -- '*.go'
```

结果：

```text
?    sogame/internal/tap       [no test files]
?    sogame/internal/platform  [no test files]
```

## 提交文件

```text
internal/platform/tap.go
internal/tap/adapter.go
internal/tap/assets.go
```
