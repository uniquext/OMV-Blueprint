FROM node:22-bookworm-slim AS frontend-builder

# npm 重试配置：网络不稳定时自动重试
ARG NPM_CONFIG_FETCH_RETRIES=5
ARG NPM_CONFIG_FETCH_RETRY_MINTIMEOUT=20000
ARG NPM_CONFIG_FETCH_RETRY_MAXTIMEOUT=120000

WORKDIR /src/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/index.html web/tsconfig.json web/vite.config.ts ./
COPY web/src/ ./src/
RUN npm run build

FROM golang:1.22-bookworm AS builder

# Go 模块代理：默认使用国内镜像加速，可通过构建参数覆盖
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct

WORKDIR /src

COPY app/go.mod app/go.sum* ./
RUN go mod download

COPY app/ ./
RUN CGO_ENABLED=0 go build -o /out/audiocleaner ./cmd/audiocleaner

FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

# apt 重试配置：网络不稳定时自动重试下载
RUN apt-get update -o Acquire::Retries=3 \
	&& apt-get install -y --no-install-recommends -o Acquire::Retries=3 ffmpeg ca-certificates tzdata \
	&& mkdir -p /app/work /app/logs /app/web/dist \
	&& chmod 1777 /app/work /app/logs \
	&& ffmpeg -version >/dev/null \
	&& ffprobe -version >/dev/null \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=frontend-builder /src/web/dist /app/web/dist
COPY --from=builder /out/audiocleaner /usr/local/bin/audiocleaner

EXPOSE 9830
ENTRYPOINT ["/usr/local/bin/audiocleaner"]
