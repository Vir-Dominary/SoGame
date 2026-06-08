# 第 8 阶段：优化赞助入口和窗口尺寸

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`ac17da2 docs: 记录 TAP 策略文档合入过程`

## 合并方式

直接 cherry-pick 当前已整理好的 sponsor/window 提交。

```powershell
git cherry-pick 800ac15
```

## 合入提交

- `800ac15 feat: 优化赞助入口和窗口尺寸`

## 合入内容

- 优化前端赞助入口展示。
- 调整应用窗口尺寸配置。
- 保留连接状态、连接详情弹窗和节点测速 UI。

## 冲突与取舍

- cherry-pick 无文本冲突。
- `frontend/src/App.jsx` 与当前详情/测速 UI 自动合并成功。
- `internal/webui/app.go` 自动合并成功，保留现有 Wails API。
- 本阶段不触碰 `internal/n2n/edge.go`，不影响 edge 管理端口优雅退出逻辑。

## 验证

已执行：

```powershell
go test ./internal/webui
npm --prefix frontend run build
git grep -n -E '<<<<<<<|>>>>>>>' -- frontend/src internal main.go
```

结果：

```text
go test ./internal/webui 通过
npm --prefix frontend run build 通过
未发现冲突标记
```

## 构建产物处理

- `npm --prefix frontend run build` 生成/更新 `frontend/dist` 产物。
- 已恢复 `frontend/dist`，不将构建产物混入本阶段提交。

## 提交文件

```text
frontend/src/App.jsx
frontend/src/index.css
internal/webui/app.go
main.go
```
