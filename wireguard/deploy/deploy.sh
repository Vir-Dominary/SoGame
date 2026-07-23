#!/usr/bin/env bash
# SoGame 控制服务器部署脚本
#
# 用法：
#   ./deploy.sh              # Docker 部署（默认 HTTP）
#   ./deploy.sh --https      # Docker 部署（HTTPS，需先准备证书）
#   ./deploy.sh --bare-metal # 裸机部署（systemd，需先编译二进制）
#   ./deploy.sh --stop       # 停止 Docker 服务
#   ./deploy.sh --status     # 查看服务状态

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()    { echo -e "${CYAN}==> $1${NC}"; }
success() { echo -e "${GREEN}  ✓ $1${NC}"; }
warn()    { echo -e "${YELLOW}  ! $1${NC}"; }
error()   { echo -e "${RED}  ✗ $1${NC}"; exit 1; }

# ========== 参数解析 ==========
MODE="docker"
HTTPS=false
ACTION="deploy"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --https)       HTTPS=true; shift ;;
        --bare-metal)  MODE="bare-metal"; shift ;;
        --stop)        ACTION="stop"; shift ;;
        --status)      ACTION="status"; shift ;;
        --help|-h)
            echo "SoGame 控制服务器部署脚本"
            echo ""
            echo "用法: ./deploy.sh [选项]"
            echo ""
            echo "选项:"
            echo "  --https        Docker 部署，启用 HTTPS（需先准备证书到 certs/）"
            echo "  --bare-metal   裸机部署（systemd，需先编译二进制）"
            echo "  --stop         停止 Docker 服务"
            echo "  --status       查看服务状态"
            echo "  --help         显示帮助"
            exit 0
            ;;
        *) error "未知参数: $1" ;;
    esac
done

# ========== Docker 部署 ==========
docker_deploy() {
    command -v docker >/dev/null 2>&1 || error "未安装 docker，请先安装: https://docs.docker.com/engine/install/"
    command -v docker compose >/dev/null 2>&1 || docker compose version >/dev/null 2>&1 || error "未安装 docker compose v2"

    # 确保 .env 存在
    if [[ ! -f .env ]]; then
        info "从模板创建 .env 配置文件"
        cp .env.example .env
        warn "请编辑 .env 设置 SOGAME_ADMIN_TOKEN 后重新运行"
        echo ""
        echo "  nano .env"
        echo ""
        exit 0
    fi

    # 检查 Admin Token 是否为默认值
    if grep -q "change-me-to-a-random-string" .env; then
        warn ".env 中 SOGAME_ADMIN_TOKEN 仍为默认值，admin API 将不安全"
        read -p "  是否继续？(y/N) " -n 1 -r
        echo
        [[ $REPLY =~ ^[Yy]$ ]] || exit 0
    fi

    if [[ "$HTTPS" == "true" ]]; then
        info "HTTPS 模式"
        # 检查证书
        if [[ ! -f certs/fullchain.pem || ! -f certs/privkey.pem ]]; then
            error "未找到 TLS 证书，请将 fullchain.pem 和 privkey.pem 放到 certs/ 目录下"
        fi
        # 临时修改 .env 中的 NGINX_CONF
        if grep -q "^NGINX_CONF=" .env; then
            sed -i 's/^NGINX_CONF=.*/NGINX_CONF=nginx-https.conf/' .env
        else
            echo "NGINX_CONF=nginx-https.conf" >> .env
        fi
        success "已切换到 nginx-https.conf"
    fi

    info "构建并启动 Docker 服务"
    docker compose up -d --build
    success "服务已启动"

    echo ""
    info "服务状态:"
    docker compose ps
    echo ""
    echo -e "${GREEN}部署完成！${NC}"
    echo "  Web UI:  http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo 'localhost')${HTTPS:+s}"
    echo "  Health:  curl http://localhost:8080/health"
}

# ========== 裸机部署 ==========
bare_metal_deploy() {
    info "裸机部署模式（systemd）"

    [[ $(id -u) -eq 0 ]] || error "请使用 root 或 sudo 运行"

    local BINARY="$SCRIPT_DIR/../server/sogame-server"
    if [[ ! -x "$BINARY" ]]; then
        # 尝试编译
        info "编译服务器二进制"
        (cd "$SCRIPT_DIR/../server" && CGO_ENABLED=0 go build -o sogame-server ./cmd/server/)
        BINARY="$SCRIPT_DIR/../server/sogame-server"
    fi

    [[ -x "$BINARY" ]] || error "编译失败或二进制不存在"

    info "安装二进制到 /usr/local/bin/"
    cp "$BINARY" /usr/local/bin/sogame-server
    chmod +x /usr/local/bin/sogame-server

    info "创建系统用户"
    if ! id -u sogame >/dev/null 2>&1; then
        useradd -r -s /usr/sbin/nologin -d /var/lib/sogame sogame
    fi

    info "创建数据目录"
    mkdir -p /var/lib/sogame /etc/sogame
    chown sogame:sogame /var/lib/sogame

    info "部署环境变量配置"
    if [[ ! -f /etc/sogame/server.env ]]; then
        cp systemd/sogame-server.env.example /etc/sogame/server.env
        warn "请编辑 /etc/sogame/server.env 设置 SOGAME_ADMIN_TOKEN"
    fi

    info "安装 systemd 服务"
    cp systemd/sogame-server.service /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable sogame-server
    systemctl restart sogame-server
    success "服务已安装并启动"

    echo ""
    info "服务状态:"
    systemctl status sogame-server --no-pager || true
    echo ""
    echo -e "${GREEN}部署完成！${NC}"
    echo "  查看日志: journalctl -u sogame-server -f"
    echo "  配置文件: /etc/sogame/server.env"
    echo "  数据目录: /var/lib/sogame/"
}

# ========== 停止 / 状态 ==========
stop_services() {
    if [[ "$MODE" == "bare-metal" ]]; then
        systemctl stop sogame-server 2>/dev/null && success "已停止 sogame-server" || warn "服务未运行"
    else
        docker compose down 2>/dev/null && success "已停止 Docker 服务" || warn "Docker 服务未运行"
    fi
}

show_status() {
    if [[ "$MODE" == "bare-metal" ]]; then
        systemctl status sogame-server --no-pager || true
    else
        docker compose ps || true
    fi
}

# ========== 主流程 ==========
case "$ACTION" in
    deploy)
        if [[ "$MODE" == "bare-metal" ]]; then
            bare_metal_deploy
        else
            docker_deploy
        fi
        ;;
    stop)    stop_services ;;
    status)  show_status ;;
esac
