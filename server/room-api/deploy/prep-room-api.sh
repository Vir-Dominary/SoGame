#!/bin/bash
# SoGame Room API 部署环境初始化脚本（幂等，可安全重复执行）。
#
# 关键修复（相比早期一次性脚本）：
#   A. ROOM_API_ENCRYPTION_KEY 复用已有值，绝不重新随机生成 —— 该密钥用于
#      加密 SQLite 里已存在的 code_ciphertext / setup_key_ciphertext，一旦
#      更换，历史房间将无法再加入（密文不可逆）。仅当 env 不存在该键时才生成。
#   B. 补齐 ROOM_API_ADMIN_TOKEN —— /rooms/{code}/disable 管理端点鉴权依赖。
#      env 已存在则复用；否则生成一个高熵令牌并写入 token 文件安全保存。
set -euo pipefail

ENV_FILE="${ROOM_API_ENV_FILE:-/root/room-api.env}"
PAT_FILE="${ROOM_API_PAT_FILE:-/root/room-api-pat.txt}"
DATA_DIR="/root/room-api-data"

mkdir -p "$DATA_DIR"

# PAT 必须已由运维预置
if [[ ! -s "$PAT_FILE" ]]; then
    echo "错误：未找到 NetBird PAT 文件 $PAT_FILE" >&2
    exit 1
fi
PAT=$(cat "$PAT_FILE")

# --- 读取/生成敏感值（复用优先）---
load_existing() {
    # 从已存在的 env 文件读指定键的值；不存在或报错则返回空。
    grep -E "^$1=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true
}

ENC_KEY=$(load_existing ROOM_API_ENCRYPTION_KEY)
if [[ -z "$ENC_KEY" ]]; then
    ENC_KEY=$(openssl rand -base64 32)
    echo "已生成新的 ROOM_API_ENCRYPTION_KEY"
else
    echo "复用已有 ROOM_API_ENCRYPTION_KEY（避免历史房间密文不可逆）"
fi

ADMIN_TOKEN=$(load_existing ROOM_API_ADMIN_TOKEN)
if [[ -z "$ADMIN_TOKEN" ]]; then
    ADMIN_TOKEN=$(openssl rand -hex 32)
    echo "已生成新的 ROOM_API_ADMIN_TOKEN"
else
    echo "复用已有 ROOM_API_ADMIN_TOKEN"
fi

# --- 写入 env（每次覆盖写，但敏感值来自上面的复用/生成）---
cat > "$ENV_FILE" <<EOF
NETBIRD_MANAGEMENT_URL=https://legengen.top
NETBIRD_PAT=$PAT
ROOM_API_ENCRYPTION_KEY=$ENC_KEY
ROOM_API_ADMIN_TOKEN=$ADMIN_TOKEN
ROOM_API_DB_PATH=/data/room-api.db
ROOM_API_RELAY_ENABLED=false
ROOM_API_TRUST_PROXY=true
EOF
chmod 600 "$ENV_FILE"

echo "env 已写入 $ENV_FILE（敏感值已脱敏展示如下）："
grep -v -E "NETBIRD_PAT|ROOM_API_ENCRYPTION_KEY|ROOM_API_ADMIN_TOKEN" "$ENV_FILE"

# 密钥与令牌的持久副本：写入独立文件便于进阶备份（与 DB 一起备份）
if [[ ! -s "$DATA_DIR/admin-token.txt" ]]; then
    echo -n "$ADMIN_TOKEN" > "$DATA_DIR/admin-token.txt"
    chmod 600 "$DATA_DIR/admin-token.txt"
fi
echo "提示：请将 $ENV_FILE 与 $DATA_DIR/room-api.db 一并备份。"