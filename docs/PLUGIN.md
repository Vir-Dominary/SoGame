# 插件系统

## 概述

SoGame 通过插件系统支持不同游戏的联机辅助工具。每个插件封装一个第三方游戏工具，在 VPN 连接成功后提供一键调用。

## 插件接口

```go
type Plugin interface {
    Meta() Meta                                      // 元数据
    Ready() bool                                     // 运行时依赖是否就绪
    CanActivate(session Session) (bool, string)      // 当前是否可操作
    Status(session Session) Status                   // 当前状态与可用操作
    ExecuteAction(actionID string, session Session) error  // 执行操作
}
```

### 各方法职责

| 方法 | 职责 | 调用时机 |
|---|---|---|
| `Meta()` | 返回插件名称、描述、运行时目录等 | 前端加载插件列表时 |
| `Ready()` | 检查 exe/dll 等运行时文件是否存在 | `Status()` 和 `CanActivate()` 内部 |
| `CanActivate()` | 返回当前能否执行操作及其原因 | `Status()` 内部，决定按钮是否可点 |
| `Status()` | 返回当前状态、提示信息、可用操作按钮 | 前端每次查询状态时 |
| `ExecuteAction()` | 执行具体操作（注入/解除/启动服务等） | 用户点击按钮时 |

### Session 上下文

```go
type Session struct {
    Connected bool   // VPN 是否已连接
    IsHost    bool   // 当前用户是否为房主
    MyIP      string // 本机 VPN IP
    HostIP    string // 房主 VPN IP（从邀请码解析）
    Community string // 群名
    Supernode string // 中心节点地址
}
```

## 如何添加新插件

### 1. 创建插件包

在 `internal/plugins/` 下新建目录，实现 `plugin.Plugin` 接口：

```go
package torchlight2

import "netjoin/internal/plugin"

type Plugin struct{}

func (p *Plugin) Meta() plugin.Meta { ... }
func (p *Plugin) Ready() bool { ... }
func (p *Plugin) CanActivate(session plugin.Session) (bool, string) { ... }
func (p *Plugin) Status(session plugin.Session) plugin.Status { ... }
func (p *Plugin) ExecuteAction(actionID string, session plugin.Session) error {
    switch actionID {
    case plugin.ActionActivate:
        // 启动服务
    case plugin.ActionDeactivate:
        // 停止服务
    }
    return nil
}
```

### 2. 注册插件

编辑 `internal/plugins/registry.go`：

```go
func All() []plugin.Plugin {
    return []plugin.Plugin{
        civ6.New(),
        torchlight2.New(), // ← 添加
    }
}
```

### 3. 放置运行时文件

将游戏工具的 exe/dll 放入 `bin/<游戏名>/` 目录，在 `Meta.RuntimeDir` 中声明路径。

### 4. 前端自动支持

前端通过 `ListPlugins()` 获取列表，`GetPluginStatus()` 获取状态和按钮，`PluginAction()` 触发操作。新插件无需修改前端代码。

## 示例：文明6 插件

```
internal/plugins/civ6/
├── plugin.go   — ExecuteAction 分发 activate/deactivate，调 exe
├── paths.go    — 运行时路径查找
bin/civ6/       — injciv6 运行时文件（构建时自动复制）
```

**操作流程**：

1. VPN 连接成功 → 前端展示"文明6 联机"卡片
2. 用户启动游戏 → 前端显示"可注入，目标房主 x.x.x.x"
3. 用户点击"注入文明6" → 插件写 `injciv6-config.txt`（含房主 IP），运行 `injciv6.exe`
4. 注入成功 → 状态变为"已注入"，显示"解除注入"按钮
5. 用户点击"解除注入" → 运行 `civ6remove.exe`
6. 关闭游戏 → 插件自动重置为"可注入"状态

## 构建

```powershell
.\build.ps1           # 完整构建 + 复制依赖到 build/bin/
.\build.ps1 -SkipBuild # 仅复制依赖
```

构建输出结构：

```
build/bin/
├── SoGame.exe
├── edge.exe
└── civ6/          ← 从 bin/civ6/ 复制
    ├── injciv6.exe
    └── ...
```
