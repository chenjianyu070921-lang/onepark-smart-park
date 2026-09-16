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
	"onepark/app/gateway-service/internal/frame"
	"onepark/app/gateway-service/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
)

var configFile = flag.String("f", "etc/gateway.yaml", "config file")

// gateway-service: M1 多协议网关.
// 当前实现 TCP 长连接接入: 设备建连 -> auth 帧认证(bcrypt 校验设备密钥)
// -> telemetry/event/status 上报投递 Kafka -> ack 回执回写 command_log.
// CoAP 接入后续引入 plgd-dev/go-coap, 与 TCP 共用同一套帧语义.
func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

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
