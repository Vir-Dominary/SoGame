# SoGame WireGuard 极速模式

基于 WireGuard 的 P2P 局域网联机系统控制平面，作为 SoGame 的"极速模式"，与现有 N2n 经典模式并行。

## 设计理念

**服务器仅做信令交换，所有游戏流量 P2P 直连。**

```
控制平面（服务器）              数据平面（P2P 直连）
┌─────────────────┐           ┌──────────────────────┐
│  控制服务器      │           │  WireGuard P2P 隧道   │
│  仅做信令交换    │ ←───→     │  游戏流量直接点对点    │
│  不转发游戏流量  │           │  不经过任何服务器      │
└─────────────────┘           └──────────────────────┘
```

用户无需理解 WireGuard，仅通过邀请码完成联机。

## 架构

### 四层组件

```
┌─────────────────────────────────────────────────────────┐
│  用户层                                                   │
│  浏览器 Web UI（创建/加入房间、查看成员、后台管理）          │
└────────────┬──────────────────────────┬────────────────┘
             │ HTTP/WS                    │ HTTP
             ▼                            ▼
┌─────────────────────┐     ┌─────────────────────────────┐
│  控制服务器           │     │  本地 Agent（用户机器）        │
│  - REST API          │     │  - 生成 WireGuard 密钥对      │
│  - WebSocket Hub     │←───→│  - 管理 WireGuard 接口        │
│  - SQLite 数据库     │     │  - STUN 探测公网端点           │
│  - IP 地址分配       │     │  - 心跳上报                    │
└─────────────────────┘     └──────────┬──────────────────┘
                                       │ wg set / wg-quick
                                       ▼
                            ┌─────────────────────────────┐
                            │  WireGuard 内核模块           │
                            │  - UDP 51820 监听             │
                            │  - 加密 P2P 隧道              │
                            │  - 虚拟 IP: 10.88.X.Y        │
                            └─────────────────────────────┘
```

### 各组件职责

| 组件 | 职责 |
|------|------|
| 控制服务器 | 房间管理、IP 分配、节点注册、WebSocket 实时通知、心跳监控 |
| 本地 Agent | 密钥管理、WireGuard 接口管理、STUN NAT 穿透、心跳上报 |
| Web UI | 创建/加入房间、成员列表、后台管理、实时状态 |
| WireGuard | 加密 P2P 隧道，游戏流量直接点对点传输 |

## 目录结构

```
wireguard/
├── server/                          # 控制服务器
│   ├── cmd/server/main.go           # 服务入口（优雅关闭、健康检查）
│   └── internal/
│       ├── api/handlers.go          # REST API 处理器
│       ├── api/handlers_test.go     # 14 个 API 单元测试
│       ├── db/database.go           # SQLite 数据层（WAL、外键、唯一索引）
│       ├── ipam/ipam.go             # IP 地址管理（10.88.X.0/24）
│       ├── models/models.go         # 数据模型
│       ├── room/manager.go          # 房间管理（并发安全）
│       └── ws/                      # WebSocket Hub + Client
├── agent/                           # 本地 Agent
│   ├── cmd/agent/main.go            # Agent 入口（HTTP API、并发安全）
│   ├── cmd/stunprobe/main.go        # STUN 探测命令行工具
│   └── internal/
│       ├── client/client.go         # 控制服务器 HTTP 客户端
│       ├── keys/                    # Curve25519 密钥对生成
│       ├── logger/logger.go         # 日志
│       ├── models/models.go         # Agent 数据模型
│       ├── stun/                    # STUN 端点发现模块
│       │   ├── protocol.go          # RFC 5389 STUN 协议实现
│       │   ├── servers.go           # 250 个公共 STUN 服务器
│       │   ├── probe.go             # 并发探测 + 延迟排序
│       │   └── protocol_test.go     # 12 个协议测试
│       ├── wg/manager.go            # WireGuard 接口管理
│       └── ws/listener.go           # WebSocket 监听器
├── web/                             # Web UI
│   └── src/
│       ├── App.jsx                  # 主应用
│       ├── api.js                   # API 客户端
│       ├── main.jsx                 # 入口
│       └── index.css                # 样式
└── deploy/                          # 部署配置
    ├── docker-compose.yml           # 编排（nginx + backend + web）
    ├── Dockerfile.server            # 后端构建
    ├── Dockerfile.web               # 前端构建
    └── nginx/
        ├── nginx.conf               # HTTP 反向代理
        └── nginx-https.conf         # HTTPS 配置
```

## 完整联机流程

### 1. 房主创建房间

```
玩家A → Web UI 点击"创建房间"
    → Agent A 生成密钥对（首次）
    → Agent A 调用 POST /api/room/create
    → 控制服务器分配子网 10.88.0.0/24，分配 IP 10.88.0.2，生成邀请码
    → Agent A 创建 WireGuard 接口（Address=10.88.0.2/24）
    → Agent A 连接 WebSocket /ws/room/{room_id}
    → Agent A 启动 stunLoop 探测公网 endpoint
    → Agent A 启动 pingLoop 定期上报 endpoint
    → Web UI 显示邀请码
```

### 2. 客人加入房间

```
玩家B → Web UI 输入邀请码，点击"加入"
    → Agent B 生成密钥对（首次）
    → Agent B 调用 POST /api/room/join
    → 控制服务器分配 IP 10.88.0.3，返回房间内现有 peer 列表（含 A 的 endpoint）
    → Agent B 创建 WireGuard 接口
    → Agent B 调用 wg set 添加 A 为 peer（endpoint + persistent-keepalive 25）
    → 控制服务器 WebSocket 广播 peer_join 到房间
    → Agent A 收到通知，调用 wg set 添加 B 为 peer
    → Agent B 启动 stunLoop + pingLoop
    → 两个 Agent 之间 WireGuard 握手，P2P 隧道建立
```

### 3. NAT 穿透

```
Agent A                          Agent B
   │                                │
   │ stunLoop → STUN 探测            │ stunLoop → STUN 探测
   │ ← 公网 endpoint = 1.2.3.4:55946 │ ← 公网 endpoint = 5.6.7.8:63516
   │                                │
   │ pingLoop 上报 endpoint          │ pingLoop 上报 endpoint
   │ ──────→ 控制服务器 ←──────────  │
   │                                │
   │           WebSocket peer_update 分发
   │ ←──────────────────────────────→│
   │                                │
   │ wg set peer <B>                │ wg set peer <A>
   │   endpoint 5.6.7.8:63516       │   endpoint 1.2.3.4:55946
   │   persistent-keepalive 25      │   persistent-keepalive 25
   │                                │
   │ ←─── WireGuard 握手 ──────────→│
   │ ←─── 游戏流量 P2P ────────────→│
```

### 4. 离开房间

```
Agent → POST /api/room/leave
    → 控制服务器删除 peer 记录
    → WebSocket 广播 peer_leave
    → 其他 Agent 调用 wg set peer remove
    → 本地 Agent 调用 wg-quick down 销毁网卡
    → 若房间变空，自动删除房间
```

## IP 地址规划

```
房间 1: 10.88.0.0/24     房间 2: 10.88.1.0/24     房间 3: 10.88.2.0/24
  ├─ 房主: .2              ├─ 房主: .2              ├─ 房主: .2
  ├─ 客人1: .3             ├─ 客人1: .3             ├─ ...
  └─ 客人2: .4             └─ ...
```

- 每房间独立 /24 子网，互不干扰
- 节点 IP 从 .2 开始分配（.1 为网关保留）
- 最多 253 节点/房间，最多 256 房间

## 数据库设计

### room 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 房间 UUID |
| invite_code | TEXT UNIQUE | 8 位邀请码 |
| network_type | TEXT | 网络类型 |
| subnet | TEXT | 子网 CIDR |
| created_at | DATETIME | 创建时间 |

### peer 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 节点 UUID |
| room_id | TEXT FK | 所属房间（外键） |
| nickname | TEXT | 昵称 |
| public_key | TEXT UNIQUE | WireGuard 公钥 |
| virtual_ip | TEXT | 虚拟 IP |
| endpoint | TEXT | 公网 endpoint（STUN 探测） |
| last_seen | DATETIME | 最后心跳时间 |

- SQLite WAL 模式，支持并发读写
- 外键约束启用，删除房间级联检查
- `(room_id, virtual_ip)` 唯一索引，防止 IP 冲突

## REST API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /health | 健康检查 |
| POST | /api/room/create | 创建房间 |
| POST | /api/room/join | 加入房间 |
| POST | /api/room/leave | 离开房间 |
| GET | /api/room/peers?room_id= | 获取节点列表 |
| POST | /api/ping | 心跳上报 endpoint |
| GET | /api/admin/stats | 管理统计 |
| GET | /api/admin/rooms | 房间列表 |
| GET | /api/admin/peers | 节点列表 |
| DELETE | /api/admin/room/{id} | 删除房间 |
| DELETE | /api/admin/peer/{id} | 踢出节点 |

## WebSocket 事件

| 事件 | 说明 |
|------|------|
| peer_join | 新节点加入房间 |
| peer_leave | 节点离开房间 |
| peer_update | 节点信息更新（endpoint 变更等） |
| room_deleted | 房间被删除 |

## STUN 端点发现

### 工作原理

1. Agent 的 `stunLoop` 协程首次立即探测，之后每 5 分钟刷新
2. 调用 `stun.DiscoverPublicIP` 并发探测 250 个公共 STUN 服务器
3. 取延迟最低的可用结果作为公网 endpoint
4. 缓存到 `lastEndpoint`，由 `pingLoop` 每 15 秒上报给控制服务器
5. 控制服务器通过 WebSocket `peer_update` 分发给房间内其他节点
6. 其他节点调用 `wg set` 更新对端地址 + `persistent-keepalive 25`

### STUN 协议实现

- 纯 Go 标准库实现 RFC 5389 Binding Request/Response
- 无外部依赖
- 支持 XOR-MAPPED-ADDRESS（RFC 5389）和 MAPPED-ADDRESS（旧版）双兼容
- 并发探测（信号量控制，默认 30 并发）
- 按延迟升序排序，返回最优结果

### 命令行工具

```bash
cd wireguard/agent
go run ./cmd/stunprobe
```

输出示例：
```
探测完成，耗时 19.5s，可用 10 / 250
stun.l.google.com:19302    164ms    141.11.146.71    141.11.146.71:55946
stun.pjsip.org:3478        195ms    141.11.146.71    141.11.146.71:63516
...
```

## 部署

### Docker Compose 一键部署

```bash
cd wireguard/deploy
docker compose up -d --build
```

服务启动后：
- nginx 监听 80/443 端口
- backend 监听 8080 端口（容器内）
- Web UI 访问 `http://服务器IP/`
- 健康检查 `http://服务器IP/health`

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| SOGAME_DB_PATH | /data/sogame.db | SQLite 数据库路径 |
| SOGAME_LISTEN | :8080 | 后端监听地址 |
| SOGAME_WEB_DIR | /web | Web 静态文件目录 |

### HTTPS 配置

1. 申请证书：`certbot certonly --standalone -d your-domain.com`
2. 复制证书到 `deploy/certs/`
3. 修改 `docker-compose.yml` 使用 `nginx-https.conf`
4. 重启：`docker compose up -d --build`

## 测试

### 服务端 API 测试

```bash
cd wireguard/server
go test ./...
```

14 个测试覆盖：房间创建/加入/离开、IP 分配顺序、心跳、管理统计、删除房间、踢出节点、重复公钥处理、多房间多节点等。

### STUN 协议测试

```bash
cd wireguard/agent
go test ./internal/stun/
```

12 个测试覆盖：Binding Request 构建、XOR-MAPPED-ADDRESS 解析、MAPPED-ADDRESS 解析、事务 ID 生成、错误处理等。

## 安全保障

| 层面 | 机制 |
|------|------|
| 隧道加密 | WireGuard ChaCha20-Poly1305 |
| 密钥交换 | Curve25519 椭圆曲线 Diffie-Hellman |
| 身份验证 | 每节点独立密钥对，公钥即身份 |
| 网络隔离 | 每房间独立 /24 子网，互不可达 |
| NAT 保活 | persistent-keepalive 25 秒 |

## 与 N2N 经典模式对比

| 维度 | N2N 经典模式 | WireGuard 极速模式 |
|------|-------------|-------------------|
| 拓扑 | Supernode 中转/边缘节点 | 纯 P2P 网状 |
| 延迟 | 中转增加 1 跳 | 直连最低延迟 |
| NAT 穿透 | n2n 自带 | STUN + Keepalive |
| 加密 | AES（可关） | ChaCha20（强制） |
| 内核加速 | 否 | 是（内核模块） |
| 配置复杂度 | 低（自动） | 中（需装 WG） |
| 跨平台 | 好 | Windows 优先 |

## 当前建设情况

### 已完成

- [x] 控制服务器 REST API（房间创建/加入/离开/节点列表/心跳）
- [x] WebSocket 实时通知（peer_join/peer_leave/peer_update/room_deleted）
- [x] SQLite 数据库（WAL 模式、外键约束、唯一索引）
- [x] IP 地址管理（10.88.X.0/24 子网分配，mutex 防竞态）
- [x] 房间管理并发安全（sync.Mutex 保护所有操作）
- [x] 重复公钥处理（自动移除旧记录，清理空房间）
- [x] 优雅关闭（SIGINT/SIGTERM 信号监听，10s 超时）
- [x] 健康检查端点
- [x] 14 个 API 单元测试全部通过
- [x] Agent 密钥对生成（RFC 7748 Curve25519，wg 命令/Go 库双路径）
- [x] Agent WireGuard 接口管理（Connect/Disconnect/AddPeer/RemovePeer）
- [x] Agent WebSocket 监听器（实时 peer 增删，room_deleted 回调）
- [x] Agent 并发安全（sync.Mutex 保护所有状态字段）
- [x] STUN 端点发现模块（RFC 5389 协议实现，250 个服务器，并发探测）
- [x] STUN 集成到 Agent pingLoop（stunLoop + discoverEndpoint）
- [x] PersistentKeepalive 25 秒（NAT 映射保活）
- [x] 12 个 STUN 协议单元测试全部通过
- [x] stunprobe 命令行探测工具
- [x] Web UI 首页（创建/加入房间）
- [x] Web UI 房间页面（成员列表、虚拟 IP、在线状态）
- [x] Web UI 后台管理（统计、房间列表、踢出成员、删除房间）
- [x] Web UI 实时 WebSocket 成员状态更新
- [x] Docker Compose 部署（nginx + backend + web）
- [x] Docker 多阶段构建（golang:1.25-alpine + node:18-alpine）
- [x] Nginx HTTP/HTTPS 反向代理配置
- [x] 健康检查、service_healthy 条件依赖
- [x] .dockerignore 排除构建产物

### 待完成

- [ ] 管理接口认证（当前 /api/admin/* 无认证）
- [ ] Agent CORS 收紧（当前 Allow-Origin: *，应限制为部署域名）
- [ ] Agent 优雅关闭（信号处理，防止僵尸节点）
- [ ] Linux/macOS 平台支持（当前仅 Windows wireguard.exe）
- [ ] 前端经典 N2N 模式集成（当前仅提示使用桌面客户端）
- [ ] 前端 WebSocket 重连机制
- [ ] 内置 STUN 服务器（当前使用公共 STUN，未来自托管）
- [ ] 对称型 NAT 兜底策略（端口转发提示）
- [ ] 日志轮转
- [ ] 输入校验（昵称长度、公钥 base64 格式）

### 整体完成度

| 维度 | 评分 | 说明 |
|------|------|------|
| 架构设计 | 85% | 三层分离清晰，模块划分合理 |
| 控制服务器 | 80% | 功能完整，缺认证 |
| Agent | 75% | STUN 已集成，缺优雅关闭和跨平台 |
| STUN 模块 | 95% | 协议完善，测试覆盖良好 |
| 前端 UI | 70% | 极速模式可用，经典模式缺失 |
| 部署运维 | 85% | Docker 完整，HTTPS 配置就绪 |
| 安全性 | 50% | 隧道加密强，管理接口缺认证 |
| **整体** | **75%** | 核心链路已打通，可进行端到端测试 |

## 技术栈

| 层 | 技术 |
|------|------|
| 后端 | Go 1.25、net/http、gorilla/websocket、modernc.org/sqlite |
| 前端 | React 18、Vite 5 |
| 数据库 | SQLite（WAL 模式） |
| 部署 | Docker Compose、Nginx、Alpine Linux |
| VPN | WireGuard（ChaCha20-Poly1305） |
| NAT 穿透 | STUN（RFC 5389，纯 Go 实现） |
| 密钥 | Curve25519（RFC 7748） |
