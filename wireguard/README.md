# SoGame 极速模式（基于 NetBird）

SoGame 的"极速模式"基于 [NetBird](https://netbird.io/) 实现，使用 WireGuard 加密 + NAT 穿透，
与现有 n2n 经典模式并行。极速模式的目标是提供更稳定的 P2P 连接和更简单的用户体验。

## 设计理念

**借用 NetBird 的成熟能力，用户只需创建/加入房间即可联机。**

```
Room API（控制平面）              数据平面（P2P 直连 / 中继）
┌─────────────────────┐         ┌──────────────────────────┐
│  Room API 服务       │         │  WireGuard P2P 隧道       │
│  房间创建/加入/成员   │ ←───→   │  游戏流量直接点对点        │
│  Setup Key 分发      │         │  NAT 穿透失败时走中继      │
└─────────────────────┘         └──────────────────────────┘
         │
         ▼
┌─────────────────────┐
│  NetBird 官方服务器  │
│  Management + Signal │
│  部署在 legengen.top │
└─────────────────────┘
```

用户无需理解 WireGuard 或 NetBird，仅通过房间码完成联机。

## 架构

### 组件层次

```
┌─────────────────────────────────────────────────────────────┐
│  用户层：Wails 桌面 UI                                       │
│  创建房间 / 加入房间 / 查看房间成员 / 离开房间                │
└────────────┬────────────────────────────────────────────────┘
             │ Wails 绑定（Express* 方法）
             ▼
┌─────────────────────────────────────────────────────────────┐
│  协调层：ExpressController (internal/webui/express.go)       │
│  房间生命周期管理、状态机、错误映射、服务健康检查              │
└────────────┬────────────────────────────────┬───────────────┘
             │                                 │
             ▼                                 ▼
┌────────────────────────┐    ┌──────────────────────────────┐
│  Room API 客户端        │    │  session.Service              │
│  (internal/roomapi)     │    │  (internal/session)           │
│  创建/加入房间、查询成员 │    │  房间状态机、注册事务、对等体  │
└────────────┬───────────┘    └────────────┬─────────────────┘
             │ HTTPS                        │ gRPC
             ▼                              ▼
┌────────────────────────┐    ┌──────────────────────────────┐
│  Room API 服务器        │    │  NetBird 守护进程（系统服务）  │
│  (部署在 legengen.top)  │    │  WireGuard 隧道管理           │
│  房间 ↔ NetBird Group   │    │  NAT 穿透 / 中继              │
│  Setup Key 生成         │    │  对等体发现                   │
└────────────────────────┘    └──────────────────────────────┘
```

### 关键设计决策

1. **不自建控制平面**：极速模式完全复用 NetBird 官方的 Management/Signal 服务器，
   避免重复造轮子。Room API 仅作为房间管理薄层，将房间映射为 NetBird Group + Setup Key。

2. **官方 MSI 打包**：极速模式打包官方 NetBird v0.74.7 MSI，首次使用时由
   `sogame-helper.exe` 通过 UAC 提权安装为 Windows 系统服务。不修改 NetBird 二进制。

3. **gRPC 通信**：SoGame 主程序通过 gRPC（127.0.0.1:41731）与 NetBird 守护进程通信，
   复用 NetBird 的 daemon.proto 接口进行注册、连接、状态查询。

4. **DPAPI 安全存储**：房间码通过 Windows DPAPI 加密存储，仅当前用户可解密。
   元数据文件（room.json）存储房间 ID、Management URL、Profile ID。

5. **简化用户流程**：不实现 NetBird 的登录/鉴权/OIDC 等功能，仅通过 Setup Key
   完成匿名注册，用户只需输入昵称 + 房间码即可联机。

## 房间生命周期

```
用户点击"创建房间"
    │
    ▼
Room API 创建房间 → 生成 NetBird Group + Setup Key
    │
    ▼
session.Service.Create() → NetBird daemon.Enroll(Setup Key)
    │
    ▼
NetBird daemon.Connect() → 建立 WireGuard 隧道
    │
    ▼
房间码加密存储 → UI 显示房间码供分享
    │
    ▼
定期刷新房间视图 → 更新成员列表、连接状态
```

## 源码结构

```
internal/
├── netbird/           # NetBird gRPC 适配器（与守护进程通信）
│   ├── adapter.go     # Adapter 接口定义
│   ├── rpc_adapter.go # gRPC 实现（连接 127.0.0.1:41731）
│   ├── version.go     # 版本检查与强制匹配
│   ├── monitor.go     # 恢复监控
│   ├── profile.go     # 管理配置文件
│   └── rpc/           # protoc 生成的 daemon.proto 代码
├── roomapi/           # Room API HTTP 客户端
│   └── client.go      # 创建/加入房间、查询成员
├── session/           # 房间会话状态机
│   ├── service.go     # 房间生命周期（创建/加入/离开/重连）
│   ├── state.go       # 状态机（NoRoom → Enrolling → Connected）
│   └── peers.go       # 对等体列表刷新
├── securestore/       # DPAPI 安全存储
│   ├── roomcode.go    # 房间码加密存储
│   ├── metadata.go    # 房间元数据存储
│   └── dpapi_windows.go # Windows DPAPI 调用
├── nbdaemon/          # NetBird 守护进程管理
│   ├── service.go     # 服务状态检查
│   ├── install.go     # MSI 安装/修复
│   ├── artifact.go    # MSI 完整性验证（SHA256 + 签名）
│   └── elevation_windows.go # UAC 提权
├── releasebuild/      # NetBird 发布元数据
│   ├── metadata.go    # 嵌入 netbird-release.json
│   └── netbird-release.json # MSI URL、SHA256、ProductCode
└── webui/
    ├── app.go         # Wails 绑定（经典模式 + 极速模式入口）
    ├── express.go     # ExpressController 协调器
    ├── express_windows.go # Windows 构造器（注入所有组件）
    └── express_other.go   # 非 Windows 桩

cmd/
└── sogame-helper/     # UAC 提权辅助程序（安装/修复/移除 NetBird MSI）
```

## 构建

```powershell
# 一键构建（自动下载 NetBird MSI）
.\scripts\build-all.ps1

# 输出：
#   build\bin\SoGame.exe          - 主程序
#   build\bin\sogame-helper.exe   - MSI 安装辅助程序
#   build\bin\edge.exe            - 经典模式 n2n
#   build\bin\netbird_installer_0.74.7_windows_amd64.msi - 官方 NetBird MSI
```

## 运行时依赖

| 组件 | 来源 | 安装方式 |
|------|------|----------|
| NetBird 守护进程 | 官方 v0.74.7 MSI | 首次使用极速模式时 sogame-helper UAC 安装 |
| WireGuard Wintun 驱动 | NetBird MSI 内含 | 随 MSI 安装 |
| Room API 服务 | sogame-netbird 部署 | 已在 legengen.top 运行 |
| NetBird Management/Signal | sogame-netbird 部署 | 已在 legengen.top 运行 |

## 与经典模式的关系

| 特性 | 经典模式 (n2n) | 极速模式 (NetBird) |
|------|---------------|-------------------|
| 协议 | n2n + TAP 网卡 | WireGuard + Wintun |
| NAT 穿透 | supernode 中继 | ICE + TURN 中继 |
| 加密 | AES（社区密钥） | ChaCha20-Poly1305 |
| 用户流程 | 邀请码 → 连接 | 房间码 → 加入 |
| 成员可见性 | 仅 IP | 房间成员列表 + 连接路径 |
| 服务依赖 | supernode | Room API + NetBird 服务器 |

## 验证进度（2026-08）

### ✅ 已完成

- **客户端集成**：`internal/netbird`（gRPC 适配器）/ `internal/roomapi` / `internal/session`（状态机+事务补偿）/
  `internal/securestore`（DPAPI）/ `internal/nbdaemon` 全部就位并随库带上单元测试。
- **单机全链路**（本地 Mock Room API + 真实 NetBird daemon，注册到 legengen.top）：
  - 创建房间 → daemon 入网（WireGuard 虚拟 IP 就绪）→ 进入房间管理界面；
  - 房间码明文展示 + 一键复制；房间成员列表 / 空状态提示；
  - 断开 / 离开房间按钮（样式与整体 UI 统一）；
  - 失败事务回滚（codes/metadata 清理）与孤儿 `sogame-room` profile 自动自愈。
- **默认指向真实控制平面**：客户端 `DefaultRoomAPIURL` 为 `https://legengen.top`（与产品语义一致），
  本地 Mock（`tools/room-api-mock`）仅作为开发期可选替身，需在 UI 设置/配置中显式切到
  `http://127.0.0.1:9099` 才会使用。

### ⏳ 待办

- **双机真实联机**：两台 Windows 各自创建/加入房间，验证成员互相可见、
  状态走到 `ConnectedP2P`（或 `ConnectedRelay`）、`ping` 通虚拟 IP、
  Disconnect/Reconnect/Leave 全流程。
- **Room API 服务端移植进 SoGame**（自包含控制平面，替换部署在 legengen.top 的服务端）。
- 诊断打包（diagnostics）、系统托盘、自动更新、多房间管理。
