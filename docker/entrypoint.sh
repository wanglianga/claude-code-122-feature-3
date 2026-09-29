#!/bin/sh
# 容器入口：等待 PostgreSQL 就绪 → 初始化（幂等）→ 启动服务
set -e

: "${KEG_DSN:=host=db user=keg password=keg dbname=kegplatform sslmode=disable}"
: "${KEG_ADDR:=:8080}"
export KEG_DSN KEG_ADDR

# 从 KEG_DSN 粗略解析连接信息供 pg 等待使用（支持 host=/tmp 与 host=host 两种形式）
PG_HOST=$(echo "$KEG_DSN" | sed -n 's/.*host=\([^ ]*\).*/\1/p')
PG_PORT=$(echo "$KEG_DSN" | sed -n 's/.*port=\([^ ]*\).*/\1/p')
PG_USER=$(echo "$KEG_DSN" | sed -n 's/.*user=\([^ ]*\).*/\1/p')
PG_PORT="${PG_PORT:-5432}"

echo "[entrypoint] 等待 PostgreSQL ${PG_HOST}:${PG_PORT} ..."
i=0
until /app/kegapp waitpg 2>/dev/null || [ "$i" -ge 60 ]; do
  i=$((i+1))
  sleep 1
done

echo "[entrypoint] 初始化数据库（幂等）"
/app/kegapp init || true

echo "[entrypoint] 启动服务 ${KEG_ADDR}"
exec /app/kegapp
