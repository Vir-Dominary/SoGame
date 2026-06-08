# rebase-test 分支从头整理方案

本文档用于从 `origin/main` 重新整理一个干净的 `rebase-test` 分支，复用当前整理方向：小型基础提交用 `cherry-pick`，独立功能分支用 `merge --squash`，TAP 大改按主题分组提交。

重点原则：`origin/main` 已包含 `fix/stop-graceful-shutdown` 的 edge 管理端口优雅退出逻辑，重新整理时必须保留，不能被旧 `develop` / TAP 提交覆盖回 `os.Interrupt + taskkill /F` 旧实现。

## 目标

- 以 `origin/main` 为唯一基底重新创建整理分支。
- 不直接 merge 整个 `origin/develop`。
- 只合入已确认需要的功能：branding/release 基础、连接状态修复、连接详情、节点测速、TAP GUID 所有权策略、赞助入口和窗口尺寸。
- 保留 `origin/main` 的 edge 管理端口断开逻辑。
- 保持安装器版本为 `1.3`，并保持 TAP 驱动由应用运行时安装，不恢复 installer 的 `install_tap.bat` 运行步骤。
- 构建产物和生成噪音不混入功能提交。

## 前置检查

```powershell
git fetch --all --prune
git status --short --branch
git branch -avv
git log --oneline --decorate -20 origin/main
```

确认点：

- `origin/main` 指向 `181800f` 或更新，并包含 `cff3bf1 fix: edge 优雅退出，解决重连时 supernode 认证冲突`。
- 工作区干净，或先保存用户未提交改动。
- 当前旧 `rebase-test` 只作为参考，不直接基于它继续修补。

## 新建整理分支

推荐保留旧分支备份，再从 `origin/main` 创建新分支。

```powershell
git switch rebase-test
git branch rebase-test-backup-before-rebuild
git switch -c rebase-test-v2 origin/main
```

如果最终仍要使用 `rebase-test` 名称，可以在验证通过后再重命名，不要一开始删除旧分支。

## 第 1 阶段：合并 nic 基础能力

这四个提交都服务同一个目标：提供 Windows 网卡查询、NetCfg 标识解析、设备启停等待和调试入口。重新整理时建议合并成一个主题提交，减少历史噪音和后续冲突点。

```powershell
git cherry-pick -n 62d03db
git cherry-pick -n 36dc99c
git cherry-pick -n 1858108
git cherry-pick -n 0b563b0
git commit -m "feat(nic): 添加 Windows 网卡查询与设备控制能力"
```

预期内容：

- `internal/nic/*`：Windows 网卡查询、NetCfgInstanceId/LUID 解析、设备启停等待。
- `internal/poll/*`：通用等待工具。
- `cmd/nicctl/main.go`：网卡调试命令。

验收：

```powershell
git grep -n "netjoin/internal" -- .
go test ./internal/nic/... ./internal/poll/...
```

## 第 2 阶段：合入 develop 基础，但保留 main 的 edge 退出逻辑

当前第一次整理中，`54e3d84` 来自旧 develop 基础提交，直接 cherry-pick 后回退了 `internal/n2n/edge.go` 的管理端口退出逻辑。这次不要直接照抄该提交。

推荐做法是先不提交地应用旧 develop 基础，再撤回 `edge.go`，最后提交。

```powershell
git cherry-pick -n 54e3d84
git restore --source=origin/main -- internal/n2n/edge.go
git restore --source=origin/main -- internal/webui/app.go
git status --short
git commit -m "chore: 合入开发版基础配置"
```

说明：

- `internal/n2n/edge.go` 必须保持 `mgmtPort`、`BuildArgs(cfg, mgmtPort)`、`-t <port>`、`sendMgmtStop()`、`terminateEdgeProcess(..., mgmtPort)`、`runTaskkill(pid, force)` 等 main 逻辑。
- `internal/webui/app.go` 同样先保留 main 版本，后续连接详情、测速、TAP ensure 会按主题重新合入。
- 如需从该提交保留 `webui` 的非冲突内容，手工挑选，不要整文件覆盖。

验收：

```powershell
git grep -n "mgmtPort\|sendMgmtStop\|terminateEdgeProcess\|runTaskkill" -- internal/n2n/edge.go
git grep -n "\"-t\"" -- internal/n2n/edge.go
git grep -n "netjoin/internal" -- .
```

## 第 3 阶段：cherry-pick release/branding

合入 release/branding 时保持版本 `1.3`，不要回退安装器名称和版本。

```powershell
git cherry-pick 7a1c5d2
```

冲突处理要求：

- `installer/sogame.iss` 保持 `#define MyAppName "SoGame"`。
- `installer/sogame.iss` 保持 `#define MyAppVersion "1.3"`。
- `installer/sogame.iss` 保持 `Source: "..\build\bin\SoGame.exe"`。
- 不添加运行 `install_tap.bat` 的 `[Run]` 步骤。
- 如 `internal/n2n/edge.go` 冲突，优先保留 main 的管理端口退出逻辑。

验收：

```powershell
git grep -n "MyAppVersion\|install_tap.bat\|Source: \"..\\build\\bin\\SoGame.exe\"" -- installer/sogame.iss
git grep -n "mgmtPort\|sendMgmtStop\|\"-t\"" -- internal/n2n/edge.go
```

## 第 4 阶段：squash merge 连接状态修复

不要直接 merge 整个 `origin/fix/status-detection` 历史，按主题 squash。

```powershell
git merge --squash origin/fix/status-detection
```

提交前处理：

- 只保留连接状态判断相关源码改动。
- 不提交 `frontend/dist/*`、`frontend/node_modules/*`、`frontend/package.json.md5` 等产物或缓存。
- 如 `internal/n2n/edge.go` 冲突，保留管理端口退出逻辑，只合入状态机解析和错误码判断修复。

```powershell
git restore --staged frontend/dist frontend/node_modules frontend/package.json.md5
git restore frontend/dist frontend/node_modules frontend/package.json.md5
git commit -m "fix: 修复连接成功后前端仍显示连接中"
```

提交说明建议包含：

```text
edge 状态码/错误码判断错误导致已连接仍停留在连接中。
```

验收：

```powershell
git grep -n "mgmtPort\|sendMgmtStop\|\"-t\"" -- internal/n2n/edge.go
go test ./internal/n2n ./internal/webui
```

## 第 5 阶段：squash merge 连接详情弹窗

```powershell
git merge --squash origin/feature/connection-details
```

提交前处理：

- 保留 `frontend/src/App.jsx`、`frontend/src/index.css`、`internal/webui/app.go` 的连接详情 API/UI。
- `frontend/wailsjs/go/*` 只有当后端 Wails API 确实变化时才提交。
- 不提交 `frontend/dist/*`、`frontend/node_modules/*`、`frontend/package.json.md5`。

```powershell
git restore --staged frontend/dist frontend/node_modules frontend/package.json.md5
git restore frontend/dist frontend/node_modules frontend/package.json.md5
git commit -m "feat: 添加连接成功详情弹窗"
```

验收：

```powershell
go test ./internal/webui
npm --prefix frontend run build
```

## 第 6 阶段：squash merge 节点测速

```powershell
git merge --squash origin/feature/node-latency
```

提交前处理：

- 保留节点列表、延迟检测 API、前端测速展示。
- 不接受该分支里旧 develop merge 带来的大范围回退。
- 如 `internal/n2n/edge.go` 冲突，保留管理端口退出逻辑，只合入 `KnownNode`、`GetKnownNodes()`、`MeasureAllNodesLatency()` 等测速相关内容。
- 不提交构建产物和缓存。

```powershell
git restore --staged frontend/dist frontend/node_modules frontend/package.json.md5
git restore frontend/dist frontend/node_modules frontend/package.json.md5
git commit -m "feat: 增加节点延迟检测和展示"
```

验收：

```powershell
git grep -n "MeasureAllNodesLatency\|GetKnownNodes\|mgmtPort\|sendMgmtStop\|\"-t\"" -- internal/n2n/edge.go
go test ./internal/n2n ./internal/webui
npm --prefix frontend run build
```

## 第 7 阶段：按主题 cherry-pick TAP/GUID 改造

TAP 改造体量较大，继续按当前 `rebase-test` 已整理好的主题提交顺序合入。每一步冲突都要优先保留前面已经确认的 edge 管理端口退出逻辑。

```powershell
git cherry-pick 1733eda
git cherry-pick 11b38bf
git cherry-pick b0adb9a
git cherry-pick 72dba80
git cherry-pick d167e43
git cherry-pick 31aa783
git cherry-pick 94f0270
git cherry-pick a024a4f
git cherry-pick 4debdb8
git cherry-pick 9d1800c
git cherry-pick 44e5724
git cherry-pick 7844c5e
```

各提交目的：

- `1733eda`：TAP GUID 查询与友好名改名。
- `11b38bf`：拆分 TAP 查询与资源定位逻辑。
- `b0adb9a`：使用 `NetCfgInstanceId` 持久化 TAP 身份。
- `72dba80`：按 GUID 策略认领和创建 SoGame TAP。
- `d167e43`：按 GUID 重启并查找 TAP。
- `31aa783`：拆分 TAP IP 配置。
- `94f0270`：拆分依赖检查。
- `a024a4f`：重启等待按 GUID 跟踪网卡。
- `4debdb8`：阻断 TAP ensure 失败并保留根因。
- `9d1800c`：添加 TAP 真实环境测试命令。
- `44e5724`：更新 TAP 安装资源和运行时安装流程。
- `7844c5e`：添加 TAP 所有权策略与真实环境测试文档。

冲突处理要求：

- `internal/n2n/edge.go`：保留 edge 管理端口退出逻辑，同时接入 TAP 的 `platform.FindTapInterfaceName()` 和测速/状态机变更。
- `internal/webui/app.go`：`Connect` 前保留 `platform.EnsureSoGameAdapter()`，同时保留连接详情/测速 API。
- `installer/sogame.iss`：保持版本 `1.3`，不恢复 `install_tap.bat` 运行步骤。
- Go 导入路径必须是 `sogame/internal/...`，不能出现 `netjoin/internal/...`。

每个 TAP 相关冲突解决后都建议执行：

```powershell
git grep -n "netjoin/internal" -- .
git grep -n "mgmtPort\|sendMgmtStop\|\"-t\"" -- internal/n2n/edge.go
```

如果某个当前 `rebase-test` 提交仍包含旧导入路径，解决冲突时直接改为 `sogame/internal/...`，不要等最后补救。

## 第 8 阶段：cherry-pick sponsor/window 剩余有效内容

`origin/develop` 的最终汇总提交 `1912fd2` 不要合入，它主要包含构建产物和会干扰 installer/TAP 策略的旧内容。只合入当前已整理出的 sponsor/window 主题提交。

```powershell
git cherry-pick 800ac15
```

验收：

```powershell
git grep -n "mgmtPort\|sendMgmtStop\|\"-t\"" -- internal/n2n/edge.go
git grep -n "netjoin/internal" -- .
```

## 第 9 阶段：不要重复当前补救提交，直接在冲突阶段修好路径

当前旧 `rebase-test` 最后有：

```text
c6ea2e2 fix: 修正整理后残留的模块导入路径
```

从头整理时不建议再 cherry-pick 这个补救提交。应在前面 TAP 提交冲突解决时直接把所有导入路径保持为 `sogame/internal/...`。

如果最终仍发现残留，再单独提交：

```powershell
git grep -n "netjoin/internal" -- .
git commit -am "fix: 修正整理后残留的模块导入路径"
```

## 最终验证

```powershell
git status --short --branch
git log --oneline --decorate -30
git grep -n "mgmtPort\|sendMgmtStop\|terminateEdgeProcess\|runTaskkill\|\"-t\"" -- internal/n2n/edge.go
git grep -n "netjoin/internal" -- .
go test ./internal/nic/... ./internal/poll/... ./internal/tap ./internal/platform ./internal/ipconfig ./internal/diagnostics ./internal/n2n ./internal/webui
npm --prefix frontend run build
wails build
```

构建后如出现 tracked 产物或生成噪音，默认恢复，不并入功能提交：

```powershell
git restore frontend/dist/index.html
git restore frontend/wailsjs/go/app/App.d.ts frontend/wailsjs/go/app/App.js frontend/wailsjs/go/models.ts
git restore go.mod
git status --short --branch
```

最终工作区应保持干净。`build/bin/SoGame.exe` 可以存在，但不要默认提交。

## 最终历史建议

理想提交结构如下：

```text
feat(nic): 添加 Windows 网卡查询与设备控制能力
chore: 合入开发版基础配置
chore: release v1.3.0, update branding and supernode address
fix: 修复连接成功后前端仍显示连接中
feat: 添加连接成功详情弹窗
feat: 增加节点延迟检测和展示
feat(nic): 支持 TAP GUID 查询与友好名改名
refactor(tap): 拆分 TAP 查询与资源定位逻辑
refactor(tap): 使用 NetCfgInstanceId 持久化 TAP 身份
refactor(platform): 按 GUID 策略认领和创建 SoGame TAP
refactor(tap): 按 GUID 重启并查找 TAP
refactor(ipconfig): 拆分 TAP IP 配置
refactor(diagnostics): 拆分依赖检查
fix(tap): 重启等待按 GUID 跟踪网卡
fix(tap): 阻断 TAP ensure 失败并保留根因
chore(tap): 添加 TAP 真实环境测试命令
chore(installer): 更新 TAP 安装资源和运行时安装流程
docs(tap): 添加 TAP 所有权策略与真实环境测试文档
feat: 优化赞助入口和窗口尺寸
```

注意：当前旧 `rebase-test` 的 `54e3d84` 和 `c6ea2e2` 是整理过程中的参考点，不应机械照搬。新的整理分支应在合入旧 develop 基础时保住 `origin/main` 的 edge 管理端口退出逻辑，并尽量避免最后再靠补救提交修路径。
