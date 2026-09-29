#!/usr/bin/env bash
# 一键启动本地开发环境：
# 1) 若无用户级 PostgreSQL（端口 5433）则 initdb 并启动
# 2) 创建 kegplatform 数据库并加载 schema + 种子数据
# 3) 启动 Go 后端（:8080，已 embed 前端构建产物）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PGDATA="$ROOT/.pgdata"
# Debian/Ubuntu 下 psql 在 /usr/bin，而 initdb/pg_ctl 在 /usr/lib/postgresql/<ver>/bin
PGBIN=""
for d in /usr/lib/postgresql/*/bin /usr/local/pgsql/bin /opt/homebrew/opt/postgresql*/bin; do
  if [ -x "$d/initdb" ]; then PGBIN="$d"; break; fi
done
if [ -z "$PGBIN" ]; then
  echo "未找到 PostgreSQL 服务端工具（initdb/pg_ctl），请先安装 postgresql 并将其 bin 加入 PATH" >&2
  exit 1
fi
export PATH="$PGBIN:$PATH"

if [ ! -d "$PGDATA" ]; then
  echo "[1/3] 初始化用户级 PostgreSQL 数据目录 $PGDATA"
  initdb -D "$PGDATA" -U node --auth=trust --encoding=UTF8 --locale=C.UTF-8
  cat >> "$PGDATA/postgresql.conf" <<EOF
port = 5433
unix_socket_directories = '/tmp'
listen_addresses = 'localhost'
EOF
fi

if ! pg_isready -h /tmp -p 5433 -U node >/dev/null 2>&1; then
  echo "[1/3] 启动 PostgreSQL（端口 5433）"
  pg_ctl -D "$PGDATA" -l "$ROOT/.pgdata/server.log" start
  sleep 2
fi

if ! psql -h /tmp -p 5433 -U node -lqt | cut -d'|' -f1 | grep -qw kegplatform; then
  echo "[2/3] 创建数据库 kegplatform"
  createdb -h /tmp -p 5433 -U node kegplatform
fi

if [ "${RESET_DB:-0}" = "1" ]; then
  echo "[2/3] 重置数据库（RESET_DB=1）"
  psql -h /tmp -p 5433 -U node postgres -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='kegplatform' AND pid<>pg_backend_pid();" >/dev/null
  dropdb -h /tmp -p 5433 -U node --if-exists kegplatform
  createdb -h /tmp -p 5433 -U node kegplatform
fi

cd "$ROOT/backend"
# go:embed 需要前端构建产物：缺失时自动构建（需 Node.js ≥ 18）
if [ ! -f web/dist/index.html ]; then
  if command -v npm >/dev/null 2>&1; then
    echo "[2.5/3] 未发现 backend/web/dist，自动构建前端"
    (cd "$ROOT/frontend" && { [ -d node_modules ] || npm install --cache "$ROOT/.npm-cache"; } && npm run build)
  else
    echo "警告：未找到 npm，且 backend/web/dist 缺失，Go embed 构建将失败。" >&2
    echo "请先在 frontend/ 执行 npm install && npm run build，或使用 docker compose up。" >&2
    exit 1
  fi
fi
# 数据库为空表时自动初始化
if [ "$(psql -h /tmp -p 5433 -U node kegplatform -tAc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")" = "0" ]; then
  echo "[2/3] 加载 schema 与种子数据"
  go run . init
fi

echo "[3/3] 启动后端 http://localhost:8080"
exec go run .
