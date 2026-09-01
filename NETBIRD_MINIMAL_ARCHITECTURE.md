# NETBIRD_MINIMAL_ARCHITECTURE.md — SoGame 精简架构（阶段 B 交付）

> 目标：把企业级 Overlay Network 平台（NetBird）裁剪成适合 SoGame 局域网游戏联机的轻量级 P2P 虚拟局域网基础设施。

---

## 1. 目标架构

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
```

## 2. 保留 / 关闭 / 不部署 / 推迟

| 类别 | 组件 | 说明 |
|---|---|---|
| ✅ 保留（核心） | Management（Peer 注册、Setup Key、Group、同组互通 Policy、Peer 列表） | SoGame 房间模型直接依赖 |
| ✅ 保留（核心） | Signal | P2P 协商必需 |
| ✅ 保留（核心） | STUN（内嵌，UDP 3478） | NAT 穿透必需；与 Relay 解耦 |
| ✅ 保留（核心） | WireGuard | 数据面 |
| ✅ 保留（必要） | 内嵌 IdP（Dex）+ 单账户模式 | combined 认证链路 |
| ✅ 保留（必要） | PAT（Room API 调 Management） | 服务间认证 |
| 🔌 服务器掌握，默认关闭 | **Relay** | 开关在 Room API（`ROOM_API_RELAY_ENABLED`，默认 false），随房间下发给客户端；客户端被动遵循，纯 P2P 优先 |
| 🚫 不部署 | **Dashboard**（netbirdio/dashboard） | SoGame 有自己的 UI |
| 🚫 不部署 | **coturn**（coturn/coturn） | combined 用内嵌 STUN，无需 TURN |
| ⏳ 推迟（代码级） | **agent-network（LLM 网关）** | 深耦合 management store/HTTP/gRPC/modules（~20 文件），未配置时惰性；见 PRUNING 计划 |
| ⏳ 推迟（客户端耦合） | upload-server / flow | 仅客户端（官方 MSI）使用；服务端镜像不含 |
| ⏳ 推迟 | proxy | 被 management reverseproxy 依赖 |
| ⏳ 后续评估 | DNS、Route、Posture、SSH、Network Resource | 深植 management，需专项重构 |

## 3. 设计原则落地

1. **纯 P2P 优先**：Relay 默认关闭；P2P 失败时明确报告"无法直连"，不自动走中继。
2. **Relay 不删除**：作为可选能力保留，配置即可恢复，用于未来高级场景。
3. **STUN 与 Relay 解耦**：关 Relay 绝不误删 STUN（NAT 穿透依赖）。
4. **配置驱动**：全部通过 combined YAML 控制，无硬编码、无 `if false`。
5. **向后兼容**：`server.relay.enabled: true` 或外部 `server.relays` 均恢复原行为。
