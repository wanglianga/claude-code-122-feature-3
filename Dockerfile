# ---------- 前端构建 ----------
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
# 产物输出到 ../backend/web/dist（与 vite.config.js 一致）
RUN npm run build

# ---------- 后端构建 ----------
FROM golang:1.19-alpine AS backend
WORKDIR /app
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/
# 用前端构建产物覆盖 embed 目录
COPY --from=frontend /app/backend/web/dist ./backend/web/dist
RUN cd backend && go build -o /out/kegplatform .

# ---------- 运行镜像 ----------
FROM alpine:3.19
RUN apk add --no-cache ca-certificates postgresql-client bash
WORKDIR /app
COPY --from=backend /out/kegplatform /app/kegplatform
COPY backend/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
EXPOSE 8080
ENTRYPOINT ["/app/entrypoint.sh"]
