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
| `NETBIRD_PAT` | ✅ | — | NetBird Management API 的个人访问令牌 |
| `ROOM_API_ENCRYPTION_KEY` | ✅ | — | 房间码/Setup Key 落库加密密钥（32 字节，可给 base64 或任意口令） |
| `ROOM_API_ADMIN_TOKEN` | ✅ | — | 管理令牌（`/rooms/{code}/disable` 用 `X-Room-Admin-Token` 校验） |
| `ROOM_API_ADDR` | | `:8080` | HTTP 监听地址 |
| `NETBIRD_MANAGEMENT_URL` | | `https://legengen.top` | 下发给客户端的 NetBird Management 地址（NetBird 控制平面，非网站域名 virdy.cn） |
| `ROOM_API_DB_PATH` | | `room-api.db` | SQLite 文件路径 |
| `ROOM_API_CREATE_RATE_PER_MINUTE` | | `5` | 每 IP 每分钟建房限流 |
| `ROOM_API_JOIN_RATE_PER_MINUTE` | | `30` | 每 IP 每分钟入房限流 |
| `ROOM_API_PEER_RATE_PER_MINUTE` | | `60` | 每 IP 每分钟成员/心跳限流 |
| `ROOM_API_MAX_BODY_BYTES` | | `4096` | 请求体上限 |
| `ROOM_API_PROVISION_CONCURRENCY` | | `2` | 建房并发（NetBird 资源创建并发） |
| `ROOM_API_RELAY_ENABLED` | | `false` | 是否允许房间使用 Relay 中继（随 enrollment 下发客户端） |
| `ROOM_API_TRUST_PROXY` | | `false` | 是否信任反向代理的 `X-Forwarded-For`（见下文） |
| `ROOM_API_OWNER_OFFLINE_AFTER` | | `5m` | 房主多久没心跳即由看门狗解散房间（`<=0` 关闭） |
| `ROOM_API_OWNER_SWEEP_INTERVAL` | | `1m` | 看门狗扫描间隔 |

## 关于 `ROOM_API_TRUST_PROXY`

限流与审计按客户端 IP 计数。默认**不信任** `X-Forwarded-For`，直接取 TCP 对端地址，
避免服务器直连公网时被伪造头绕过限流。

**仅当** Room API 前置了可信反向代理（traefik / nginx）才应设为 `true`，并确保代理
会覆盖（而非追加）`X-Forwarded-For` 头。参考部署：

- `sogame-server`（123.56.254.224）：traefik + 域名 `virdy.cn` + Let's Encrypt，经代
  理转发，应设 `ROOM_API_TRUST_PROXY=true`。
- 若将其作为容器直连 80 端口暴露，则保持默认 `false`。

## 密钥与数据备份

`ROOM_API_ENCRYPTION_KEY` 用于加密 SQLite 中的房间码与 Setup Key。密钥一旦丢失，
库里已加密字段将**不可逆**（无法恢复房间码/Setup Key）。请：

1. 生成后妥善保存，并与 `room-api.db` 一起纳入备份；
2. 轮换密钥前需先迁移全部密文（当前版本未提供在线迁移，需停服手动重加密或接受
   丢弃旧房间）。

生成 32 字节 base64 密钥：

```bash
openssl rand -base64 32
```

## 命令行开关

| 开关 | 说明 |
|------|------|
| `-healthcheck` | 校验配置与 SQLite 完整性后退出（供容器 healthcheck / 探活） |
| `-disable-default-policy` | 禁用 NetBird 账号级 Default All-to-All 策略后退出（一次性迁移） |