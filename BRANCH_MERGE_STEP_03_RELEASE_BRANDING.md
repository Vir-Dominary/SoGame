# 第 3 阶段：合入 release/branding

## 操作时间

- 2026-06-08

## 起点

- 分支：`rebase-test`
- 起点提交：`64a8e8e chore: 合入开发版基础配置`

## 合并方式

无提交应用 release/branding 提交，解决 `internal/webui/app.go` 冲突后提交。

```powershell
git cherry-pick -n 7a1c5d2
```

## 合入提交

- `7a1c5d2 chore: release v1.3.0, update branding and supernode address`

## 合入内容

- 应用名称、窗口标题、Wails 配置、README、默认配置和 Windows metadata 切换为 SoGame 1.3。
- 安装器脚本从 `installer/netjoin.iss` 重命名为 `installer/sogame.iss`。
- 更新前端基础 branding 文案。
- 更新 logger/app config 里的应用元信息。

## 冲突与取舍

- `internal/webui/app.go` 出现内容冲突，冲突点集中在错误返回形式。
- 冲突解决时保留 release 提交中带 `%w` 的错误包装写法，例如 `fmt.Errorf("保存配置失败: %w", err)`。
- 保留第 2 阶段已建立的 `sogame/internal/...` 导入路径。
- 未改动 `internal/n2n/edge.go`，继续保留 `origin/main` 的 edge 管理端口优雅退出逻辑。

## installer 处理

- 保持 `#define MyAppName "SoGame"`。
- 保持 `#define MyAppVersion "1.3"`。
- 将主程序来源调整为 `Source: "..\build\bin\SoGame.exe"`。
- 保留 TAP 资源复制，包括 `tap\install_tap.bat` 作为资源文件。
- 移除 `[Run]` 中执行 `install_tap.bat` 的步骤。
- 移除 installer 里的 TAP 检测函数，TAP 驱动安装由应用运行时负责。

## 验证

已执行：

```powershell
git grep -n -E '<<<<<<<|>>>>>>>' -- '*.go'
git grep -n -E '<<<<<<<|>>>>>>>' -- frontend/src/*.jsx
git grep -n -E '\[Run\]|Filename: "\{app\}\\tap\\install_tap\.bat"' -- installer/*.iss
git grep -n -E 'mgmtPort|sendMgmtStop|terminateEdgeProcess|runTaskkill|"-t"' -- internal/n2n/edge.go
go test ./internal/nic/... ./internal/poll/... ./internal/n2n ./internal/webui
```

结果：

```text
ok   sogame/internal/nic    (cached)
ok   sogame/internal/poll   (cached)
?    sogame/internal/n2n    [no test files]
?    sogame/internal/webui  [no test files]
```

## 提交前暂存文件

```text
.gitignore
README.md
assets/default.yaml
build/windows/info.json
frontend/index.html
frontend/package.json
frontend/src/App.jsx
installer/netjoin.iss -> installer/sogame.iss
internal/config/app_config.go
internal/config/config.go
internal/logger/logger.go
internal/webui/app.go
wails.json
```
