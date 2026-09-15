package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/event-dispatcher/internal/config"
)

// event-dispatcher: M1 后台进程, 无 HTTP/gRPC server
// 订阅 EMQX 设备消息, 分类后投递到 Kafka (online/offline/telemetry/event).
func main() {
	var configFile string
	flag.StringVar(&configFile, "f", "etc/dispatcher.yaml", "config file")
	flag.Parse()

	var c config.Config
	conf.MustLoad(configFile, &c, conf.UseEnv())

	logx.Infof("event-dispatcher starting, broker=%s, kafka=%s", c.EMQXBroker, c.KafkaBrokers)

	// TODO: 初始化 paho MQTT 订阅 + Kafka producer
	// go mqttSubscribe()
	// go kafkaForward()

	// 阻塞等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logx.Info("event-dispatcher shutting down")
}
