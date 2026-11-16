# Room API 服务器

SoGame 极速模式（NetBird/WireGuard）的控制平面薄层。将"房间"映射为 NetBird 的
Group + Setup Key + Policy，向上暴露房间创建/加入/成员查询/房主回收（解散、心跳、
离线看门狗）等 REST 接口。

本目录是独立的 Go module（`sogame/server/room-api`），与仓库根部的客户端 module
（`sogame`）解耦，可独立构建、独立部署。

## 构建

```bash
cd server/room-api
CGO_ENABLED=1 go build -trimpath -o room-api ./cmd/room-api
```

依赖 CGO（`github.com/mattn/go-sqlite3`），构建机需安装 C 工具链（Windows 用
MinGW-w64，Linux/macOS 用 gcc）。

### 运行测试

```bash
go test ./...
```

## Docker 构建与运行

```bash
cd server/room-api
docker build -t sogame-room-api .
docker run --rm -p 8080:8080 \
  -v "$PWD/data:/data" \
  -e NETBIRD_PAT='...' \
  -e ROOM_API_ENCRYPTION_KEY='...' \
  -e ROOM_API_ADMIN_TOKEN='...' \
  sogame-room-api
```

`/data` 挂载目录用于持久化 SQLite（`room-api.db`）。容器以非 root 用户运行。

## 环境变量

| 变量 | 必填 | 默认 | 说明 |
|------|:---:|------|------|
| `NETBIRD_PAT` | ✅ | — | NetBird Management API 的个人访问令牌（全账号权限，爆炸半径=整个 NetBird 账号） |
| `ROOM_API_ENCRYPTION_KEY` | ✅ | — | Setup Key 落库加密密钥：`base64(32B)`（推荐，`openssl rand -base64 32`）或 32 字符原文，其他取值按 SHA256 派生（base64 合法但非 32B 将直接报错） |
| `ROOM_API_ADMIN_TOKEN` | | 空（禁用管理端点） | 管理令牌（`/rooms/{code}/disable` 用 `X-Room-Admin-Token` 校验；空值时该端点恒 401） |
| `ROOM_API_CODE_PEPPER` | | 空（自动派生） | 房间码 HMAC 查询键的 pepper；空时从加密密钥域分离派生，显式配置可独立轮换 |
| `ROOM_API_ADDR` | | `:8080` | HTTP 监听地址 |
| `NETBIRD_MANAGEMENT_URL` | | `https://legengen.top` | 下发给客户端的 NetBird Management 地址（NetBird 控制平面，非网站域名 virdy.cn） |
| `ROOM_API_DB_PATH` | | `room-api.db` | SQLite 文件路径 |
| `ROOM_API_CREATE_RATE_PER_MINUTE` | | `5` | 每 IP 每分钟建房限流 |
| `ROOM_API_JOIN_RATE_PER_MINUTE` | | `30` | 每 IP 每分钟入房限流 |
| `ROOM_API_PEER_RATE_PER_MINUTE` | | `60` | 每 IP 每分钟成员/心跳限流 |
| `ROOM_API_MAX_BODY_BYTES` | | `4096` | 请求体上限 |
| `ROOM_API_PROVISION_CONCURRENCY` | | `2` | 建房并发（NetBird 资源创建并发） |
| `ROOM_API_RELAY_ENABLED` | | `false` | UI 层中继开关：是否承认中继连接为"已连接"（随 enrollment 下发客户端，见下文"两级 relay 开关"） |
| `ROOM_API_TRUST_PROXY` | | `false` | 是否信任反向代理的 `X-Forwarded-For`（见下文） |
| `ROOM_API_OWNER_OFFLINE_AFTER` | | `5m` | 房主多久没心跳即由看门狗解散房间（`<=0` 关闭） |
| `ROOM_API_OWNER_SWEEP_INTERVAL` | | `1m` | 看门狗扫描间隔 |

## 关于 `ROOM_API_TRUST_PROXY`

限流与审计按客户端 IP 计数。默认**不信任** `X-Forwarded-For`，直接取 TCP 对端地址，
避免服务器直连公网时被伪造头绕过限流。

设为 `true` 时取 XFF **最右值**（部署模型是"恰好一跳可信代理"：traefik 把真实
对端 IP 追加到 XFF 末尾并保留客户端自带的首值——取首值等于取攻击者可伪造的值，
全部 per-IP 限流会被绕过）。**绝不要在多跳代理或代理不追加真实 IP 的拓扑下设
`true`**。

审计日志的 `path` 字段只记录路由模板（如 `/rooms/:code/peers`），绝不记录原始路径——
路径中的房间码即入会凭证，明文落盘会让任何能读日志的人加入任意活跃房间。
404 类请求（扫描/垃圾路径）按 1/64 采样记录，防日志洪泛。

参考部署：

- `sogame-server`（123.56.254.224）：traefik + 域名 `virdy.cn` + Let's Encrypt，经代
  理转发，应设 `ROOM_API_TRUST_PROXY=true`。
- 若将其作为容器直连 80 端口暴露，则保持默认 `false`。

## 两级 relay 开关（重要）

中继（relay）涉及**两个独立层面**，部署时必须保持对齐：

| 层 | 开关载体 | 控制什么 | 如何打开 | 如何关闭（默认） |
|----|---------|---------|---------|----------------|
| **数据面** | traefik 的 `/relay` 路由 label | NetBird 内置 relay service 是否可达；关 = P2P 打洞失败时**真的连不上**（无中继候选） | compose 的 netbird-server 段保留 `traefik...PathPrefix(\`/relay\`)` label | 部署模板移除该 label → relay 不可达 |
| **UI 层** | `ROOM_API_RELAY_ENABLED` | 客户端是否把中继连接显示为"已连接"；关 = 显示红色"无法 P2P 直连"提示 | env 设 `true` | 默认 `false` |

**对齐规则**（不一致会产生误导）：

| 数据面 | UI 层 | 效果 |
|--------|-------|------|
| 开 | `true` | 中继可用且如实显示"已连接 · 中继"（测试期推荐） |
| 关 | `false` | 真无中继，P2P 失败即连不上，UI 如实提示（生产默认） |
| 开 | `false` | ⚠️ 中继实际可用但 UI 报"无法直连"（误导，勿用） |
| 关 | `true` | ⚠️ UI 显示可中继但实际连不上（误导，勿用） |

部署建议：**生产服务器默认关闭**（两层全关，节省中继带宽）；低配/测试服务器或 NAT
恶劣环境占比高的区域可**选择性打开**（两层全开）。切换数据面开关需同时调整
traefik label 与 env，并重启容器。

## 密钥与数据备份

`ROOM_API_ENCRYPTION_KEY` 是唯一的 AES-256-GCM 主密钥，用于加密 SQLite 中的：

- `setup_key_ciphertext` — Setup Key 明文（成员加入时解密下发）

Room 匹配靠 `code_hash` 独立完成。自 2026-11 起 `code_hash = HMAC-SHA256(pepper,
房间码)`（pepper 默认从主密钥域分离派生，`ROOM_API_CODE_PEPPER` 可独立指定）——
旧版的无盐 SHA256 在 DB 泄露后可对 60bit 房间码空间离线枚举；旧数据在首次被查询
时自动在线迁移为 HMAC，无需停服。**房主令牌**（`owner_token`）只存 SHA256、常数时间
比对，同样不依赖主密钥。

密钥一旦更换，受影响的是：

1. **无法再加入历史房间** — `Join` 解密 `setup_key_ciphertext` 失败，换钥前创建的
   房间新成员无法加入；
2. **幂等重放失效** — 老房间的 Create 幂等重放因 `code_ciphertext` 解密失败报错；
3. 敏感字段（房间码 / Setup Key）的解密能力不可逆丢失。

因此：**绝不可"不迁移密文就换密钥"**。请：

1. 生成后妥善保存，并与 `room-api.db` 一起纳入备份；
2. 轮换密钥前需先迁移全部密文（当前版本未提供在线迁移，需停服手动重加密或接受
   丢弃旧房间）。

生成 32 字节 base64 密钥：

```bash
openssl rand -base64 32
```

## 部署环境初始化（幂等脚本）

`deploy/prep-room-api.sh` 用于生成/对齐 `/root/room-api.env`。相比早期的一次性脚本，
它修复了两个隐患：

- **密钥复用**：`ROOM_API_ENCRYPTION_KEY` 若已存在则复用、绝不重新随机生成；
- **补 `ROOM_API_ADMIN_TOKEN`**：管理端点 `/rooms/{code}/disable` 的鉴权令牌，缺失则生成并落盘到
  `room-api-data/admin-token.txt`。

脚本可安全重复执行（idempotent）。PAT 仍需手工预置到 `ROOM_API_PAT_FILE`
（默认 `/root/room-api-pat.txt`）。

## 命令行开关

| 开关 | 说明 |
|------|------|
| `-healthcheck` | 校验配置与 SQLite 连通性（轻量 `SELECT 1`）后退出（供容器 healthcheck / 探活） |
| `-disable-default-policy` | 禁用 NetBird 账号级 Default All-to-All 策略后退出（一次性迁移） |

## 强制部署步骤：禁用账号级 Default All-to-All 策略

NetBird 账号默认存在名为 `Default` 的全账号互通放行策略。本服务全靠每房间一个
Group+Policy 实现隔离——**只要 Default 策略仍 enabled，所有房间的 peer 在数据面
全账号静默互通（房间隔离形同虚设，且无任何用户侧可见症状）**。

新部署 / 重建 NetBird 账号后必须执行一次：

```bash
room-api -disable-default-policy
```

启动时服务会自检：若 Default 策略仍 enabled，会在日志中写入
`default_policy_still_enabled` 审计告警（不自动禁用——该策略可能被账号其他用途
依赖）。