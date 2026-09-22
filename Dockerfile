# OnePark 统一多阶段构建模板(全仓复用, 零重复)
# 构建上下文必须固定为仓库根(保证 go.work 全模块可见), 各服务通过 build.args 区分.
# 在 deploy/docker-compose.yml 中用法:
#   build:
#     context: ..                 # 仓库根
#     dockerfile: Dockerfile       # 本文件
#     args:
#       SVC_DIR: app/auth-service  # 服务目录(相对仓库根)
#       MAIN: auth.go              # main 包入口文件
#       PORT: 8088                 # 监听端口
#       CFG: auth-api.yaml         # etc 下配置文件名
# 未传 args 时使用下方默认值(以 auth-service 为例).

ARG SVC_DIR=app/auth-service
ARG MAIN=auth.go
ARG PORT=8088
ARG CFG=auth-api.yaml

FROM golang:1.26 AS builder
# 上游阶段也需要 args 来拼路径
ARG SVC_DIR
ARG MAIN
# 构建期模块代理(默认国内 goproxy.cn, 可被 --build-arg GOPROXY=... 覆盖); 避免容器内 go mod download 拉取失败.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=$GOPROXY
WORKDIR /src
# 复制整个仓库(经根 .dockerignore 排除 .git/docs/deploy 等非构建文件), 保证 go.work 完整可解析.
COPY . .
WORKDIR /src/${SVC_DIR}
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/app ${MAIN}

# 运行阶段: 精简 alpine, busybox 自带 wget 用于健康检查.
# 注意: 每个 FROM 阶段都要重新 ARG 声明, 跨阶段不会自动传递.
FROM alpine:3.20
ARG SVC_DIR
ARG PORT
ARG CFG
ENV APP_CFG=${CFG}
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/app /app/app
# 防御: 先建空目录, 某些服务可能没有 etc/ 时也不报错
RUN mkdir -p /app/etc
COPY --from=builder /src/${SVC_DIR}/etc /app/etc
EXPOSE ${PORT}
# 用 shell + exec 形式, 使 ENV 变量生效且 Go 进程成为 PID1(正确接收 SIGTERM 优雅退出).
ENTRYPOINT ["/bin/sh", "-c", "exec /app/app -f etc/$APP_CFG"]
