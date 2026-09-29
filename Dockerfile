# syntax=docker/dockerfile:1
# ---------- 1) 构建前端 ----------
FROM node:18-alpine AS frontend
WORKDIR /fe
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---------- 2) 构建后端（含 embed 前端产物） ----------
FROM golang:1.21-alpine AS backend
WORKDIR /src
COPY backend/ ./
# 用前端构建产物覆盖 embed 目录
COPY --from=frontend /backend/web/dist ./web/dist
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /kegapp .

# ---------- 3) 运行镜像 ----------
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 app
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=backend /kegapp /app/kegapp
COPY docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
USER app
EXPOSE 8080
ENTRYPOINT ["/app/entrypoint.sh"]
