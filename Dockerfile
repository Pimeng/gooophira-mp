# syntax=docker/dockerfile:1
#
# Phira MP 服务端（Go 实现）容器镜像。
#
# 多阶段构建：golang 编译出纯静态二进制（CGO_ENABLED=0，无动态链接），
# 运行阶段用带 CA 证书的最小 alpine 镜像（访问上游 Phira API 走 HTTPS，必须有证书）。
#
# 构建：
#   docker build -t phira-mp:local .
#   docker build -t phira-mp:local --build-arg VERSION=$(git describe --tags --always) .   # 可选注入 git 版本
#
# 运行（数据/配置持久化到具名卷）：
#   docker run -d --name phira-mp -p 12346:12346 -p 12347:12347 \
#     -e ADMIN_TOKEN=changeme -v phira-data:/data phira-mp:local

# ---------- 构建阶段 ----------
FROM golang:1.26-alpine AS build
WORKDIR /src

# 依赖层缓存：先拷依赖清单下载，再拷源码，源码变动不会使依赖层失效。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 版本号默认走内嵌的 internal/version/VERSION；传 --build-arg VERSION=... 可用 ldflags 覆盖。
ARG VERSION=""
# 纯 Go、静态、无 CGO；保留符号和 DWARF 调试信息。
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/Pimeng/gooophira-mp/internal/version.injected=${VERSION}" \
      -o /out/phira-mp ./cmd/server

# ---------- 运行阶段 ----------
FROM alpine:3.21

# ca-certificates：上游 Phira API（HTTPS）认证/谱面/成绩必需；
# tzdata：日志时间戳按时区显示（可经 TZ 环境变量设定）。
# busybox 自带 wget，供下方 HEALTHCHECK 使用，无需额外安装。
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S phira \
    && adduser -S -G phira -h /data phira

COPY --from=build /out/phira-mp /usr/local/bin/phira-mp

# 工作目录即数据目录：config / logs / record / cache / admin_data.json 均相对于此。
WORKDIR /data
RUN mkdir -p /data/config && chown -R phira:phira /data
USER phira

# 容器内默认开启 HTTP 服务（GUI/管理接口/健康检查依赖它）；均可经 -e 覆盖。
ENV HTTP_SERVICE=true \
    HTTP_PORT=12347 \
    PORT=12346 \
    HOST=:: \
    TZ=Asia/Shanghai

EXPOSE 12346 12347

# 健康检查命中公开的 /room-creation/config（无需 token），HTTP 服务正常即 200。
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- "http://127.0.0.1:${HTTP_PORT}/room-creation/config" >/dev/null 2>&1 || exit 1

# 首次运行自动生成 config/server.yaml；已有 server_config.yml 的数据卷继续兼容旧格式。
ENTRYPOINT ["phira-mp"]
