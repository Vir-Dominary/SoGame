# syntax=docker/dockerfile:1

# 构建阶段：需要 CGO（依赖 github.com/mattn/go-sqlite3）。
FROM golang:1.25 AS build

WORKDIR /src

# 先复制依赖清单，利用层缓存。
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建（静态链接 + 去除调试信息）。
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/room-api ./cmd/room-api

# 运行阶段：仅保留可执行文件与 CA 证书。
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/*

# 非 root 运行。
RUN useradd --system --uid 10001 --create-home roomapi
USER roomapi

WORKDIR /data
ENV ROOM_API_ADDR=":8080" \
    ROOM_API_DB_PATH="/data/room-api.db"

COPY --from=build /out/room-api /usr/local/bin/room-api

EXPOSE 8080

# 健康检查：依赖内置的 healthcheck 流程（校验配置与 SQLite 完整性）。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/room-api", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/room-api"]