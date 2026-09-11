package main

import (
	"flag"
	"fmt"
	"io"
	"net"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/gateway-service/internal/config"
)

// gateway-service: M1 多协议网关
// 当前仅提供 TCP 接入骨架, CoAP 接入后续引入 plgd-dev/go-coap 实现.
func main() {
	var configFile string
	flag.StringVar(&configFile, "f", "etc/gateway.yaml", "config file")
	flag.Parse()

	var c config.Config
	conf.MustLoad(configFile, &c)

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", c.Host, c.Port))
	if err != nil {
		logx.Must(err)
	}
	defer ln.Close()
	logx.Infof("gateway-service tcp listening on %s:%d", c.Host, c.Port)

	for {
		conn, err := ln.Accept()
		if err != nil {
			logx.Errorf("accept error: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				logx.Errorf("read error: %v", err)
			}
			return
		}
		logx.Infof("recv %d bytes from %s", n, conn.RemoteAddr())
		// TODO: 协议解析 -> 设备认证 -> 上报 event-dispatcher/Kafka
	}
}
