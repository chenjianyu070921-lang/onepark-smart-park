# OnePark user-manage 多阶段构建
# 构建阶段采用 go workspace 模式(go.work 引用全仓库模块), 故构建上下文必须为仓库根.
FROM golang:1.26 AS builder
WORKDIR /src
# 复制整个仓库(经 .dockerignore 排除 .git/docs 等非构建文件), 保证 go.work 完整可解析.
COPY . .
WORKDIR /src/app/user-manage
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/user-manage usermanage.go

# 运行阶段: 精简 alpine, busybox 自带 wget 用于健康检查.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/user-manage /app/user-manage
COPY --from=builder /src/app/user-manage/etc /app/etc
EXPOSE 8086
ENTRYPOINT ["/app/user-manage", "-f", "etc/usermanage-api.yaml"]
