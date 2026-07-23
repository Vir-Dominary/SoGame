# SoGame 控制服务器部署

## 架构

```
                    ┌─────────────┐
  用户浏览器 ──────► │   Nginx     │ :80 / :443
                    │  (Web UI)   │
                    └──────┬──────┘
                           │ 反向代理
                    ┌──────▼──────┐
                    │ sogame-server│ :8080
                    │  (Go 二进制) │
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │   SQLite     │ /data/sogame.db
                    └─────────────┘
```

- **Nginx**：提供 Web 管理面板静态文件 + 反向代理 API/WebSocket
- **sogame-server**：Go 编写的控制服务器，负责房间管理、节点协调、WebSocket 信令
- **SQLite**：持久化房间和节点数据（自动创建，无需手动建表）

## 快速开始（Docker）

### 前提

- Docker 24+ 及 Docker Compose v2
- 服务器开放 80/443 端口（或自定义端口）

### 步骤

```bash
cd wireguard/deploy

# 1. 创建配置文件
cp .env.example .env

# 2. 设置 Admin Token（必须）
nano .env
# 将 SOGAME_ADMIN_TOKEN 改为随机字符串
# 生成随机 token：openssl rand -hex 32

# 3. 一键部署
./deploy.sh
# 或手动：docker compose up -d --build

# 4. 验证
curl http://localhost:8080/health
# 期望输出：{"status":"ok"}
```

访问 `http://<服务器IP>/` 即可打开 Web 管理面板。

## HTTPS 部署

### 1. 获取 TLS 证书

将证书文件放到 `certs/` 目录：

```bash
certs/
├── fullchain.pem   # 证书链
└── privkey.pem     # 私钥
```

**Let's Encrypt 免费证书**：

```bash
# 安装 certbot
sudo apt install certbot

# 获取证书（需先将域名 DNS 指向服务器）
sudo certbot certonly --standalone -d sogame.example.com

# 拷贝到 deploy 目录
cp /etc/letsencrypt/live/sogame.example.com/fullchain.pem deploy/certs/
cp /etc/letsencrypt/live/sogame.example.com/privkey.pem deploy/certs/
```

### 2. 修改 nginx-https.conf

编辑 `nginx/nginx-https.conf`，将 `server_name _;` 改为你的域名：

```nginx
server_name sogame.example.com;
```

### 3. 启动 HTTPS 模式

```bash
./deploy.sh --https
# 或手动编辑 .env：NGINX_CONF=nginx-https.conf
# 然后 docker compose up -d --build
```

## 裸机部署（systemd）

适用于不使用 Docker 的环境。

### 1. 编译二进制

```bash
cd wireguard/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o sogame-server ./cmd/server/
```

> `CGO_ENABLED=0` 使用纯 Go 的 SQLite 驱动（modernc.org/sqlite），无需 C 编译器。

### 2. 运行部署脚本

```bash
cd ../deploy
sudo ./deploy.sh --bare-metal
```

脚本会自动：
- 拷贝二进制到 `/usr/local/bin/sogame-server`
- 创建 `sogame` 系统用户
- 创建数据目录 `/var/lib/sogame/`
- 安装 systemd 服务和配置文件

### 3. 配置 Admin Token

```bash
sudo nano /etc/sogame/server.env
# 修改 SOGAME_ADMIN_TOKEN
sudo systemctl restart sogame-server
```

### 4. 常用命令

```bash
sudo systemctl status sogame-server    # 查看状态
sudo systemctl restart sogame-server   # 重启
journalctl -u sogame-server -f         # 查看日志
```

### 5. 配置 Nginx 反向代理（可选）

裸机部署如需 Web 管理面板和 HTTPS，单独安装 Nginx 并使用 `nginx/nginx.conf` 或 `nginx/nginx-https.conf` 作为配置模板。

## Admin Token 说明

| 场景 | 行为 |
|------|------|
| `SOGAME_ADMIN_TOKEN` 未设置 | `/api/admin/*` 返回 **403 Forbidden**（完全禁用） |
| 请求无 `Authorization` 头 | 返回 **401 Unauthorized** |
| Token 不匹配 | 返回 **401 Unauthorized** |
| Token 正确 | 正常返回数据 |

**Web 面板使用**：访问管理页面 → 输入 Admin Token → 认证后可查看统计、房间列表、用户列表，支持删除房间和踢出用户。Token 存储在浏览器 `localStorage` 中。

**API 直接调用**：

```bash
# 查看统计
curl -H "Authorization: Bearer <your-token>" http://localhost:8080/api/admin/stats

# 删除房间
curl -X DELETE -H "Authorization: Bearer <your-token>" http://localhost:8080/api/admin/room/<room_id>
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SOGAME_DB_PATH` | `/data/sogame.db` | SQLite 数据库路径 |
| `SOGAME_LISTEN` | `:8080` | HTTP 监听地址 |
| `SOGAME_WEB_DIR` | `/web` | 静态文件目录（可选） |
| `SOGAME_ADMIN_TOKEN` | *(空)* | Admin API 认证令牌（**必须设置**） |

## 目录结构

```
wireguard/deploy/
├── .env.example              # 环境变量模板（Docker）
├── .gitignore                # 排除 .env 和证书
├── Dockerfile.server         # 服务器镜像构建
├── Dockerfile.web            # Web 面板镜像构建
├── docker-compose.yml        # Docker 编排
├── deploy.sh                 # 一键部署脚本
├── README.md                 # 本文档
├── certs/                    # TLS 证书目录
│   └── .gitkeep
├── nginx/
│   ├── nginx.conf            # HTTP 反向代理配置
│   └── nginx-https.conf      # HTTPS 反向代理配置
└── systemd/
    ├── sogame-server.service # systemd 服务文件
    └── sogame-server.env.example  # 环境变量模板（裸机）
```

## 常见问题

### Q: docker compose build 失败？

确保 Docker 版本支持 BuildKit（Docker 23+），或启用：
```bash
export DOCKER_BUILDKIT=1
```

### Q: WebSocket 连不上？

检查 Nginx 配置中 `/ws/` 路径的 `proxy_read_timeout` 是否足够长（默认 86400 秒）。

### Q: SQLite 数据库损坏？

```bash
# Docker
docker compose exec backend ls -la /data/
# 备份后删除，重启会自动重建
cp /data/sogame.db /data/sogame.db.bak
rm /data/sogame.db
docker compose restart backend
```

### Q: 如何更新到新版本？

```bash
# Docker
cd wireguard/deploy
git pull
docker compose up -d --build

# 裸机
cd wireguard/server
git pull
CGO_ENABLED=0 go build -o sogame-server ./cmd/server/
cd ../deploy
sudo ./deploy.sh --bare-metal
```
