# 第 5 阶段：squash 合入连接详情弹窗

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`ff3000b fix: 修复连接成功后前端仍显示连接中`

## 合并方式

使用 squash 合入连接详情分支，再过滤旧基底和产物变化。

```powershell
git merge --squash origin/feature/connection-details
```

## 合入来源

- `origin/feature/connection-details`
- 主要提交：`ed4c8d9 feat: 连接成功后增加详情弹窗`

## 合入内容

- `internal/webui/app.go`：新增 `ConnectionDetails` 结构和 `GetConnectionDetails()` Wails API。
- `internal/n2n/edge.go`：新增导出的 `LookupNodeName()`，供详情 API 将 supernode 地址解析为节点名。
- `frontend/src/App.jsx`：已连接状态下新增“详情”按钮、详情弹窗、虚拟 IP 复制、赞助链接入口。
- `frontend/src/index.css`：新增详情弹窗样式。
- `frontend/wailsjs/go/app/*` 和 `frontend/wailsjs/go/models.ts`：同步新增 Wails 绑定。

## 冲突与取舍

- `frontend/src/App.jsx` 出现内容冲突，冲突点位于 `handleOpenAbout()` 后的详情函数插入位置。
- 已保留详情弹窗相关 `handleOpenDetails()`、`handleCopyIP()` 和已连接状态下的“详情”按钮。
- `installer/sogame.iss`、`internal/config/config.go`、`internal/n2n/edge.go` squash 时出现旧基底冲突；先恢复当前 HEAD，再只手工保留详情所需的 `LookupNodeName()`。
- 已过滤 `.trae`、`frontend/dist/*`、`frontend/node_modules/*`、`frontend/package.json.md5` 等无关文件。
- 暂不提前合入节点测速阶段的 `GetKnownNodes()` API；`GetNodes()` 仍保持当前静态列表。
- 未改动 edge 断开逻辑，管理端口优雅退出继续保留。

## 验证

已执行：

```powershell
git grep -n -E '<<<<<<<|>>>>>>>' -- frontend/src internal frontend/wailsjs installer
go test ./internal/n2n ./internal/webui
npm --prefix frontend run build
git grep -n -E 'mgmtPort|sendMgmtStop|"-t"' -- internal/n2n/edge.go
```

结果：

```text
?    sogame/internal/n2n    [no test files]
?    sogame/internal/webui  [no test files]
frontend build passed
```

构建后已恢复 `frontend/dist/*` 产物变化。

## 提交前暂存文件

```text
frontend/src/App.jsx
frontend/src/index.css
frontend/wailsjs/go/app/App.d.ts
frontend/wailsjs/go/app/App.js
frontend/wailsjs/go/models.ts
internal/n2n/edge.go
internal/webui/app.go
```
