#!/usr/bin/env bash
# 容器入口：等待 PostgreSQL 就绪 → 首次启动自动建表+灌种子 → 启动服务
set -euo pipefail

: "${KEG_DSN:=host=db port=5432 user=keg dbname=kegplatform password=kegpass sslmode=disable}"
: "${KEG_ADDR:=:8080}"
export KEG_DSN KEG_ADDR

# 从 DSN 提取连接参数（供 pg_isready / psql 使用）
DB_HOST="$(sed -n 's/.*host=\([^ ]*\).*/\1/p' <<< "$KEG_DSN")"
DB_PORT="$(sed -n 's/.*port=\([^ ]*\).*/\1/p' <<< "$KEG_DSN")"
DB_USER="$(sed -n 's/.*user=\([^ ]*\).*/\1/p' <<< "$KEG_DSN")"
DB_NAME="$(sed -n 's/.*dbname=\([^ ]*\).*/\1/p' <<< "$KEG_DSN")"
DB_PASS="$(sed -n 's/.*password=\([^ ]*\).*/\1/p' <<< "$KEG_DSN")"
export PGPASSWORD="$DB_PASS"

echo "等待 PostgreSQL ${DB_HOST}:${DB_PORT} ..."
for i in $(seq 1 60); do
  if pg_isready -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" >/dev/null 2>&1; then
    echo "PostgreSQL 已就绪"
    break
  fi
  sleep 1
  if [ "$i" = "60" ]; then
    echo "PostgreSQL 连接超时" >&2
    exit 1
  fi
done

echo "初始化数据库（幂等）..."
/app/kegplatform init

echo "启动精酿桶追溯平台 on ${KEG_ADDR}"
exec /app/kegplatform
