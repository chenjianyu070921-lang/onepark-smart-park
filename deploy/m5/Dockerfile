# syntax=docker/dockerfile:1.7
# ============================================================================
# M5（运营招商 + 指挥调度）通用多阶段构建
#
# 四个入口共用一个 Dockerfile：它们同属一个 go.work 工作区，构建步骤完全一致，
# 差异只有「编哪个 main 包 / 挂哪份配置」——用 ARG 参数化即可，不必复制四份。
#
# 构建上下文必须是**仓库根目录**（不是本目录）：
#   docker build -f deploy/m5/Dockerfile --build-arg SERVICE=leasing-service -t onepark/leasing-service:local .
#
# 也可直接由 deploy/m5/docker-compose.m5.yml 驱动（推荐），见该文件与同目录 README.md。
# ============================================================================

ARG GO_VERSION=1.26

# ---------- 构建阶段 ----------
FROM golang:${GO_VERSION}-alpine AS builder

# SERVICE = 该服务 **main 包**所在目录，相对 app/ ——
# 通常是服务目录名（如 leasing-service），也可能是子目录：
#   leasing-service 的 gRPC 入口 main 包在 app/leasing-service/grpcserver/，
#   故该镜像要传 SERVICE=leasing-service/grpcserver。
#   ⚠️ 传错会编出**另一个入口**的二进制：拿 HTTP 二进制去读 gRPC 配置会报
#      `field "Port" is not set`(rest.RestConf 要 Port, RpcServerConf 不要)，然后无限重启。
ARG SERVICE
RUN test -n "${SERVICE}" || { echo "必须指定 --build-arg SERVICE=<main 包所在目录, 相对 app/>"; exit 1; }

# 国内网络下默认走 goproxy.cn；有直连条件时用 --build-arg GOPROXY=https://proxy.golang.org,direct 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

# 服务通过 module 名引用 common/ 与 proto/，而 go.work 又列出了全部 23 个模块 ——
# 因此必须把整个**工作区**拷进来，只拷单个服务目录会让 go.work 指向不存在的路径。
# 体积由根目录 .dockerignore 控制（已排除 .git / bin / dist / docs / deploy / *.md）。
COPY . .

# BuildKit 缓存把「模块下载」与「编译缓存」分开：
# 改代码不会重新下载依赖，改依赖也不会触发全量重编。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server "./app/${SERVICE}"

# ---------- 运行阶段 ----------
# 用 alpine 而非 scratch：服务用 time.Local 解析日期（如租赁起租日 "2006-01-02"），
# 缺 tzdata 时 time.Local 会退化成 UTC，跨零点会算错一天。
FROM alpine:3.20

# ca-certificates：访问 HTTPS 上游；tzdata：见上；
# netcat-openbsd：提供可用的 `nc -z` 做 TCP 健康检查（busybox 的 nc 不支持 -z）。
RUN apk add --no-cache ca-certificates tzdata netcat-openbsd \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app
COPY --from=builder /out/server /app/server

# 非 root 运行
RUN adduser -D -u 10001 appuser
USER appuser

# 配置由 compose 以只读卷挂载到 /app/etc/，config 变更无需重建镜像
ENTRYPOINT ["/app/server"]
CMD ["-f", "/app/etc/config.yaml"]
