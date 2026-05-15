# 忽游 SoGame

轻量级 P2P 虚拟组网工具，基于 n2n 实现。无需公网 IP，两步加入同一房间即可互联。

## 功能

- **房间制组网** — 创建房间并生成邀请链接，对方粘贴即可加入，无需手动配置 IP 和密钥
- **P2P 打洞** — 基于 n2n 的 supernode 架构，支持 NAT 穿透
- **自动密钥** — 创建房间时自动生成加密密钥，邀请链接中包含完整连接信息
- **TAP 驱动自动安装** — 首次连接时自动检测并安装 TAP 网络适配器
- **连接状态监控** — 实时显示连接状态、群内节点数和网络诊断信息
- **管理员权限自动请求** — 网络操作需要提权时自动触发 UAC

## 安装

从 [sogame-365](https://sogame-365.pages.dev/) 下载最新安装包，运行即可。

安装程序会自动处理 TAP 驱动和运行环境，无需额外操作。

> 仅支持 Windows x64

## 使用

### 创建房间

1. 输入房间名（社区标识）
2. 选择一个公用节点
3. 点击「创建房间」
4. 点击「生成房间链接」，将链接分享给对方

### 加入房间

1. 粘贴房间链接
2. 点击「加入房间」
3. 连接成功后，双方处于同一子网（10.10.10.0/24），可直接 ping 通

### 截图

<!-- 截图占位 -->
```
[主界面截图]
[创建房间截图]
[连接状态截图]
```

## 技术栈

| 层 | 技术 |
|---|------|
| 前端 | React |
| 后端 | Go |
| 框架 | Wails v2 |
| 组网 | n2n (edge) |
| 驱动 | TAP-Windows Adapter V9 |
| 打包 | Inno Setup 6 |

## 从源码构建

前置依赖：

- Go 1.22+
- Node.js 18+
- Wails CLI v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

```bash
git clone https://github.com/vir-dominary/SoGame.git
cd SoGame
wails build
```

编译产物位于 `build/bin/SoGame.exe`。

## 免责声明

本项目仅供学习和研究使用。使用者需遵守所在地区的法律法规，因不当使用造成的任何后果由使用者自行承担。

本项目基于 n2n 开源项目，n2n 的使用同样需遵循其开源协议。

## 开源协议

本项目采用 [MIT License](LICENSE) 开源。

n2n 组件遵循其自身的开源协议（GPLv3）。

## 作者

**vir_dominary**

- GitHub: [https://github.com/vir-dominary](https://github.com/vir-dominary)
- Bilibili: [https://space.bilibili.com/454851989](https://space.bilibili.com/454851989)
