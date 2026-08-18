ARG RUNTIME_IMAGE=debian:bookworm-slim

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

FROM ${RUNTIME_IMAGE}

ARG DEBIAN_FRONTEND=noninteractive

# 默认基础镜像需要安装运行依赖；隔离集成可复用已验证且已包含依赖的本地运行镜像。
ARG RUNTIME_DEPS_PREINSTALLED=false
# bookworm-slim does not carry a trust store. Seed the verified builder trust
# bundle so the first HTTPS package transaction remains certificate-validated.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g; s|http://security.debian.org|https://security.debian.org|g' /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources 2>/dev/null || true \
	&& if [ "${RUNTIME_DEPS_PREINSTALLED}" != "true" ]; then \
		for attempt in 1 2 3; do \
			apt-get update -o Acquire::Retries=10 -o Acquire::http::Timeout=30 \
				&& apt-get install -y --no-install-recommends -o Acquire::Retries=10 -o Acquire::http::Timeout=30 --fix-missing ffmpeg ca-certificates curl tzdata \
				&& break; \
			[ "${attempt}" = "3" ] && exit 1; \
			rm -rf /var/lib/apt/lists/*; \
		done; \
	fi \
	&& mkdir -p /app/work /app/logs /app/web/dist \
	&& chmod 1777 /app/work /app/logs \
	&& rm -rf /app/coverage \
	&& ffmpeg -version >/dev/null \
	&& ffprobe -version >/dev/null \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=frontend-builder /src/web/dist /app/web/dist
COPY --from=builder /out/audiocleaner /usr/local/bin/audiocleaner

EXPOSE 9830
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD curl -fsS http://127.0.0.1:9830/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/audiocleaner"]
