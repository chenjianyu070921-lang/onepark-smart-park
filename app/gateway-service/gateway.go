package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"onepark/app/gateway-service/internal/config"
	"onepark/app/gateway-service/internal/coap"
	"onepark/app/gateway-service/internal/frame"
	"onepark/app/gateway-service/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
)

var configFile = flag.String("f", "etc/gateway.yaml", "config file")

// gateway-service: M1 多协议网关.
// TCP 长连接接入: 设备建连 -> auth 帧认证(bcrypt 校验设备密钥)
// -> telemetry/event/status 上报投递 Kafka -> ack 回执回写 command_log.
// CoAP 接入已实现(plgd-dev/go-coap, UDP 5683 + 可选 DTLS 5684),
// 与 TCP 共用同一套 frame.Session 帧语义, CoAP.Enabled 默认关.
func main() {
	flag.Parse()

	var c config.Config
	// conf.UseEnv() 必填: go-zero 默认不做 ${VAR} 环境变量展开。
	// 本服务 etc/gateway.yaml 引用 ${MYSQL_DSN}/${KAFKA_BROKERS}/${COAP_DTLS_PSK},
	// 不启用则以字面量生效 -> 数据库/ broker 全部连向名为 "${MYSQL_DSN}" 的虚假地址。
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	ctx := svc.NewServiceContext(c)
	defer ctx.Close()

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", c.Host, c.Port))
	if err != nil {
		logx.Must(err)
	}
	defer ln.Close()
	logx.Infof("gateway-service tcp listening on %s:%d", c.Host, c.Port)

	// 进程退出时通知所有连接会话停止
	taskCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// CoAP 接入(与 TCP 共用 frame.Session 帧语义): CoAP.Enabled=false 时直接跳过.
	if err := coap.Start(taskCtx, ctx); err != nil {
		logx.Must(err)
	}

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logx.Info("gateway-service shutting down")
		cancel()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if taskCtx.Err() != nil {
				return
			}
			logx.Errorf("accept error: %v", err)
			continue
		}
		go frame.NewHandler(ctx).Serve(taskCtx, conn)
	}
}
