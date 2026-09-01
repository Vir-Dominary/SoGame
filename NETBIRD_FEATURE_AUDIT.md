# SoGame / NetBird 架构审计报告（阶段 A · 第 2 版）

> 审计日期：2026-08-23
> 审计对象：
> - SoGame 客户端 + Room API：`C:\Gitclone\git\SoGame`（分支 `feature/wireguard`）
> - NetBird 服务端 fork：`C:\GitClone\Git\NetBird`（完整官方源码）
> 状态：**仅审计，未做任何功能删除或生产配置修改**

---

## 0. 关键结论（先读这一节）

第 1 版报告误判"工作区不含 NetBird 服务端"。现更正：**NetBird 服务端完整 fork 位于 `C:\GitClone\Git\NetBird`**，模块路径仍是官方 `github.com/netbirdio/netbird`，目录结构完整（`management/`、`signal/`、`relay/`、`stun/`、`dns/`、`route/`、`idp/`、`client/`、`combined/`、`infrastructure_files/` 等）。

**最重要的架构事实：SoGame 用的不是分离式部署，而是 combined 单镜像。**

- SoGame 的 `internal/releasebuild/netbird-release.json` 里 `serverImage = netbirdio/netbird-server:0.74.7`。
- 这个镜像对应 NetBird 的 **combined 模式**（`combined/` 目录）：Management + Signal + Relay + STUN **跑在同一个进程里，复用同一个端口**（Relay WebSocket 挂在 `/relay` 路径）。
- 部署模板 `infrastructure_files/docker-compose.yml.tmpl` 是**分离式**（dashboard/signal/relay/management/coturn 五个容器），但 SoGame 实际用的是 combined 镜像，二者是两套不同的部署形态。

**这直接决定了 Relay 开关的实现位置：`combined/cmd/config.go` + `combined/cmd/root.go`。**

而且——**combined 模式内部其实已经有 Relay 开关的雏形**：

```go
// combined/cmd/root.go
func (s *serverInstances) createRelayServer(cfg *CombinedConfig, tlsSupport bool) error {
    if !cfg.Relay.Enabled {          // ← Relay.Enabled 已经存在
        return nil                    // ← 关闭时根本不创建 relay server
    }
    ...
}

// combined/cmd/root.go createCombinedHandler
case r.URL.Path == "/relay":
    if relayAcceptFn != nil {
        handleRelayWebSocket(...)
    } else {
        http.Error(w, "Relay service not enabled", http.StatusNotFound)  // ← 关闭时 404
    }
```

Management 下发 Relay 也已经是条件式的：

```go
// management/internals/shared/grpc/conversion.go
if config.Relay != nil && len(config.Relay.Addresses) > 0 {   // ← 空则不下发
    relayCfg = &proto.RelayConfig{...}
}
```

**缺口在于：combined 的 `Relay.Enabled` 是内部字段（`yaml:"-"`），用户无法在配置文件里直接关闭，且 `applyRelayDefaults` 默认把它置为 `true`。** 所以需要一个"一等公民"的用户可配开关。

---

## 1. 当前实际架构

```text
┌───────────────────────────────────────────────────────────────┐
│ 玩家机器（Windows）                                             │
│  SoGame.exe（Wails）                                            │
│   └─ 极速模式：gRPC(127.0.0.1:41731) ──► NetBird daemon（官方 MSI）│
└───────────────────────────────────────────────────────────────┘
                    │ HTTPS（创建/加入房间/查成员）
                    ▼
┌───────────────────────────────────────────────────────────────┐
│ Room API（SoGame 仓库 server/room-api，部署 123.56.254.224）      │
│  房间码 ↔ Group + Setup Key + Policy 薄映射层                    │
└──────────────────────┬────────────────────────────────────────┘
                       │ HTTP API（PAT Token）
                       ▼
┌───────────────────────────────────────────────────────────────┐
│ NetBird combined 单进程（netbird-server 镜像，123.56.254.224）     │
│  Management(含内嵌 IdP) + Signal + Relay + STUN，单端口复用       │
│   ├─ /api/*      Management HTTP API                            │
│   ├─ gRPC        Peer 登录/同步                                  │
│   ├─ /relay      Relay WebSocket（可选）                          │
│   └─ UDP 3478    内嵌 STUN（可选）                                │
└───────────────────────────────────────────────────────────────┘
```

### 1.1 SoGame 实际触达 NetBird 的面（来自 SoGame 仓库代码）

- **客户端**（`internal/netbird/` + `internal/netbird/rpc/`）：只调用 daemon 的 `Status / Login / Up / Down / Logout / GetActiveProfile / SubscribeEvents`，消费 Network Map 的 Management/Signal/LocalPeer/Peer 字段（含 `Relayed`/`RelayAddress`，仅用于展示"直连/中继"）。
- **Room API**（`server/room-api/internal/netbird/client.go`）：只调用 Management HTTP API 的 `/api/groups`、`/api/setup-keys`、`/api/policies`、`/api/peers`。

### 1.2 结论

SoGame 对 NetBird 的依赖**非常窄**：Management（Groups/SetupKeys/Policies/Peers）+ Signal + STUN + WireGuard + Relay（当前被动展示）。

---

## 2. Relay 完整调用链（目标 1 的核心）

### 2.1 combined 模式下的 Relay 链路

```text
配置文件（YAML，combined）
  server:
    exposedAddress: ...        # 公开地址
    authSecret: ...            # relay 认证密钥
    stunPorts: [3478]          # 内嵌 STUN 端口
    relays: { addresses: [] }  # 外部 relay（设置则关闭本地 relay）
        │
        ▼
combined/cmd/config.go: ApplySimplifiedDefaults()
  applyRelayDefaults()      → 无外部 relay 时  c.Relay.Enabled = true
  autoConfigureClientSettings() → Management.Relays.Addresses = [rels://host]
        │
        ▼
combined/cmd/root.go: createRelayServer()
  if !Relay.Enabled → return（不创建 relay server）
  else → 创建 relay server（WebSocket，挂 /relay）
        │
        ▼
management: ToManagementConfig() → nbconfig.Config.Relay = {Addresses, Secret, TTL}
        │
        ▼
management/internals/shared/grpc:
  server.go → 若 Relay.Addresses 非空，GenerateRelayToken()
  conversion.go → 若 Relay 非空且 Addresses 非空，NetbirdConfig.Relay 下发
  token_mgr.go → 若 relayCfg 非空，定时刷新 relay token
        │
        ▼
客户端（NetBird daemon）：收到 NetbirdConfig.Relay → P2P 失败时 fallback 到 relay
```

### 2.2 现有开关的缺口（要实现"配置关 Relay"需要改的地方）

| 位置 | 现状 | 需要改 |
|---|---|---|
| `combined/cmd/config.go` `ServerConfig` | 无用户可配的 relay 开关；`Relay` 是 `yaml:"-"` 内部字段 | 加一级开关（如 `server.relay.enabled`） |
| `applyRelayDefaults()` | 无外部 relay 时强制 `Relay.Enabled = true` | 尊重开关，disabled 时保持 false |
| `autoConfigureClientSettings()` | 无外部 relay 时强制写入 `Management.Relays.Addresses` | disabled 时不写（保持空 → Management 不下发） |
| `Validate()` | 本地 relay 时必须提供 `authSecret` | 关 relay 时不再强制 authSecret |
| `root.go` `createRelayServer` | `if !Relay.Enabled return` | **已正确，无需改** |
| `root.go` `/relay` 路由 | 关时返回 404 | **已正确，无需改** |
| `conversion.go` | Addresses 空则不下发 | **已正确，无需改** |
| 客户端 | 无 relay 配置时视为 disabled（有上游回归测试 `TestToNetbirdConfig_RelayInvariant` 守护） | **已正确，无需改** |

### 2.3 推荐方案（Relay Feature Flag）

在 combined 的 `ServerConfig` 增加一等开关，**默认关闭**（符合"纯 P2P 优先"）：

```yaml
server:
  exposedAddress: "https://sogame.example.com:443"
  relay:
    enabled: false        # 默认关闭；true 时恢复原有 Relay 能力
  stunPorts: [3478]       # STUN 保留（NAT 穿透必需，与 Relay 无关）
```

实现点（全部在 `combined/cmd/config.go`，约 20 行改动）：

1. `ServerConfig` 增加 `Relay *RelayToggle yaml:"relay"`，`RelayToggle{Enabled bool}`。
2. `applyRelayDefaults`：若 `!relay.Enabled` 直接返回（不置 `Relay.Enabled=true`）。
3. `autoConfigureClientSettings`：relay disabled 时不写入 `Management.Relays.Addresses`。
4. `Validate`：`authSecret` 仅当 relay enabled 时强制。
5. 日志：enabled=false 时 `log.Infof("Relay server disabled by configuration")`；enabled=true 时 `log.Infof("Starting embedded relay server")`。

**切换方式：改配置 → 重启服务 → 生效，无需改源码。** `enabled: true` 时完整恢复 Relay（Server 启动、URL 下发、客户端 fallback）。

### 2.4 方案对比

| 方案 | 复杂度 | 可维护性 | Docker 友好 | 推荐度 |
|---|---|---|---|---|
| A. combined YAML `server.relay.enabled`（默认 false）+ 环境变量覆盖 | 低 | 高 | 高 | **首选** |
| B. 仅环境变量 `NETBIRD_RELAY_ENABLED=false` | 低 | 中 | 高 | 次选 |
| C. 分离式部署里删 relay 容器 + traefik 路由 | 中 | 低 | 中 | 不适用（SoGame 用 combined） |

---

## 3. 功能使用矩阵（基于 SoGame + NetBird fork 双端代码）

> "SoGame 是否依赖"指 SoGame 客户端/Room API 代码；"位置"指 NetBird fork 内模块。

| NetBird 功能 | 当前是否使用 | SoGame 是否依赖 | 删除风险 | 位置 | 建议 |
|---|---|---|---|---|---|
| Management：Peer 注册/授权 | 是 | 高 | 高 | management/ | 保留 |
| Management：Setup Key | 是 | 高 | 高 | management/ | 保留 |
| Management：Group | 是（房间=组） | 高 | 中 | management/ | 保留 |
| Management：Policy（同组互通） | 是 | 高 | 中 | management/ | 保留（已是"组内 accept"最小用法） |
| Management：Peer 列表 API | 是 | 高 | 低 | management/ | 保留 |
| Signal | 是 | 高 | 高 | signal/ | 保留（P2P 协商核心） |
| STUN（内嵌/外部） | 是（间接） | 高 | 高 | stun/ + combined | 保留（勿因关 Relay 误删） |
| WireGuard | 是 | 高 | 高 | client/（内核集成） | 保留 |
| Relay | 被动展示 | 中 | 低 | relay/ + combined | **默认关闭 + 配置开关** |
| TURN / coturn | 否 | 无 | 低 | infrastructure_files（coturn） | combined 模式用内嵌 STUN，无需 coturn |
| DNS（nameserver/自定义域） | 否 | 无 | 中 | dns/ + management | B 类：后续评估 |
| Routes / Exit Node / 子网 | 否 | 无 | 中 | route/ + management | B 类：后续评估 |
| Posture Checks | 否 | 无 | 低 | management | B 类：后续评估 |
| SSH | 否 | 无 | 低 | management | B 类：后续评估 |
| Network Resources | 否 | 无 | 低 | management | B 类：后续评估 |
| Dashboard（Web UI） | 否 | 无 | 低 | 独立镜像 | 不部署（SoGame 有自己 UI） |
| IDP / OIDC（内嵌 Dex） | 是（间接） | 高 | 高 | idp/ + combined | 保留（combined 单账户模式必需） |
| PAT（Personal Access Token） | 是（Room API 用它调 Management） | 高 | 高 | management | 保留 |
| agent-network（LLM 网关） | 否 | 无 | 低 | agent-network/ | A 类：建议移除（与游戏联机无关） |
| upload-server / proxy / flow | 否 | 无 | 低 | 各目录 | A/B 类：后续评估 |

---

## 4. 逐模块审计要点

### A. Management（management/）
SoGame 只用：Group、SetupKey、Policy（同组 accept）、Peer 列表、内嵌 IdP（单账户模式）、PAT 认证。
**未用**：用户管理、多账户、DNS 管理、Route、Posture、SSH、Network Resource、AgentNetwork。这些都在 management 服务内，删除需拆源码，**建议 B 类（后续评估），非本轮目标**。

### B. Signal（signal/）
P2P 连接协商核心，SoGame 强依赖（客户端读 `SignalConnected`）。**必须保留，不可简化**。

### C. STUN（stun/ + combined 内嵌）
combined 模式内嵌 STUN（默认 3478），也可配外部 `server.stuns`。SoGame 依赖 NAT 穿透，**必须保留**。**与 Relay 完全独立**（Relay 关闭不影响 STUN）。

### D. Relay（relay/ + combined）
见 §2。**不删除，默认关闭 + 配置开关**。

### E. TURN
SoGame **零引用**。combined 模式用内嵌 STUN 即可，**无需 coturn**。分离式模板里的 coturn 对 SoGame 无用。**明确区分：STUN（保留）≠ TURN（不用）≠ Relay（默认关）**。

### F. DNS（dns/ + management DNS）
SoGame 用固定虚拟 IP（100.x）通信，不需要内置 DNS。但 DNS 深植于 management + client，**删除复杂度高、风险中**。**B 类：后续评估，本轮不动**。

### G. Routes（route/ + management Route）
SoGame 是纯 LAN 联机，不需要子网路由/Exit Node。删除不影响 Peer 间直接 WireGuard 通信。**B 类**。

### H. Access Control / Policies
SoGame 已用最小 Policy（同组 accept）+ `DisableDefaultPolicy`（关默认 all-to-all）。这是"房间=组"互通的关键，**保留**。不建议用 Room API 完全替代 Policy（会破坏 Network Map 的默认连通性模型）。

### I. Identity / Authentication
combined 模式用**内嵌 IdP（Dex）+ 单账户模式**，Room API 用 PAT 调 Management。这是 SoGame 的认证链路，**必须保留**。不存在"重复认证系统"（SoGame 侧无独立用户库，靠 Setup Key 匿名注册 + PAT 服务间认证）。

### J. Dashboard
SoGame 有自己的 UI，**不需要 NetBird Dashboard**。combined 模式下 Dashboard 是独立镜像，**不部署即可**（无需改源码）。

### K. Metrics / Observability
combined 内嵌 metrics 端口（默认 9090）+ healthcheck（默认 9000）。**保留 healthcheck**（生产必需）；metrics 可保留（消耗低）。

---

## 5. 依赖关系分析（本次要动的 Relay）

```
[Relay]

入口：
  combined/cmd/root.go: createRelayServer / createCombinedHandler / startServers
  combined/cmd/config.go: applyRelayDefaults / autoConfigureClientSettings

主要模块：
  relay/           （relay server 本体：WebSocket/QUIC listener、握手、store）
  stun/            （内嵌 STUN，relay 可选携带）
  shared/relay/auth/  （relay HMAC 认证）

依赖：
  combined config ─► Relay.Enabled
       │
       ├─► root.go createRelayServer（Enabled=false → 不启动）
       └─► Management.Relays.Addresses
              │
              ├─► ToManagementConfig → nbconfig.Relay
              ├─► grpc conversion.go → NetbirdConfig.Relay（下发）
              └─► grpc token_mgr.go → relay token 定时刷新

删除复杂度：低（只是加一个 flag，不改 relay/ 内部）
预计影响：关 Relay 后客户端 Network Map 无 Relay，P2P 失败时保持"无法直连"而非走中继
推荐策略：Feature Flag（server.relay.enabled，默认 false）
```

---

## 6. 推荐的最终精简架构

```text
                ┌─────────────┐
                │   SoGame    │
                │   Backend   │
                └──────┬──────┘
                       │
             ┌─────────┴─────────┐
             ↓                   ↓
        Room Service        NetBird combined
        (房间=组+Key)       （单镜像，单端口复用）
                                │
                  ┌─────────────┼─────────────┐
                  ↓             ↓             ↓
             Management       Signal         STUN
             (含内嵌IdP)      (P2P协商)     (NAT穿透)
                  │
                  ↓
              WireGuard
                  │
          ┌───────┴───────┐
          ↓               ↓
       Player A ◄──P2P──► Player B

Relay：默认关闭（server.relay.enabled=false），可选一键开启
TURN/Dashboard/DNS/Route/SSH/Posture/agent-network：不部署 / 后续裁剪
```

---

## 7. 最终修改计划（阶段 B 草案，待确认后执行）

```text
Phase 1  Relay 开关（combined 单镜像内）
         combined/cmd/config.go：加 server.relay.enabled（默认 false）
         combined/cmd/root.go：日志提示 + 复用现有 Enabled 分支
         → 构建自定义 netbird-server 镜像，替换 SoGame 引用的官方镜像

Phase 2  裁剪 A 类（agent-network、upload-server、proxy 等与联机无关模块，评估后）

Phase 3  裁剪 B 类（DNS/Route/Posture/SSH，逐个评估）

Phase 4  测试：Relay off（P2P 通/断 Relay 后 P2P 失败正确报错）↔ Relay on（可回退）

Phase 5  构建镜像 + 部署验证
```

---

## 8. 阶段 A 完成确认

本阶段仅更新了 `NETBIRD_FEATURE_AUDIT.md`，**未改动 NetBird fork 与 SoGame 仓库的任何源码/配置**。

> 注：因沙箱限制无法读取 `C:\GitClone\Git\NetBird` 的 git 元数据（branch/diff），未能确认 fork 是否已有 SoGame 自定义提交；从已读文件看，代码为官方 netbirdio/netbird 风格，未见 SoGame 改动痕迹。若 fork 已有本地改动，请告知，我会据此调整 Relay 开关的落点。

**等待人工确认。**

下一步可选：
- **A. 仅实现 Relay 开关**（combined 内加 `server.relay.enabled`，默认 false）
- **B. Relay 开关 + 裁剪 A 类模块**
- **C. Relay 开关 + 裁剪 A/B 类模块**
- **D. 你指定模块后再执行**
