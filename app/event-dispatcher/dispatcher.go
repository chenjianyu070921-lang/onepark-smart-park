package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"onepark/app/event-dispatcher/internal/config"
	"onepark/app/event-dispatcher/internal/dispatch"
	"onepark/app/event-dispatcher/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
)

// event-dispatcher: M1 后台进程, 无 HTTP/gRPC server
// 订阅 EMQX 设备上报消息, 分类后投递到 Kafka, 供 M3 告警/M5 大屏消费.
func main() {
	var configFile string
	flag.StringVar(&configFile, "f", "etc/dispatcher.yaml", "config file")
	flag.Parse()

	var c config.Config
	conf.MustLoad(configFile, &c)

	ctx := svc.NewServiceContext(c)
	defer ctx.Producer.Close()

	// 使用已展开环境变量的 config
	c = ctx.Config

	handler := dispatch.NewHandler(ctx.Producer, ctx.Resolver)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.EMQXBroker)
	opts.SetClientID(c.EMQXClientId)
	opts.SetAutoReconnect(true)
	opts.SetCleanSession(false)
	if c.EMQXUsername != "" {
		opts.SetUsername(c.EMQXUsername)
		opts.SetPassword(c.EMQXPassword)
	}
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		// 断线重连后自动恢复订阅
		token := client.Subscribe(c.EMQXTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
			if err := handler.Dispatch(context.Background(), msg.Topic(), msg.Payload()); err != nil {
				logx.Errorf("消息处理失败: topic=%s, err=%v", msg.Topic(), err)
			}
		})
		token.Wait()
		if token.Error() != nil {
			logx.Errorf("订阅失败: topic=%s, err=%v", c.EMQXTopic, token.Error())
		} else {
			logx.Infof("已订阅: %s", c.EMQXTopic)
		}
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		logx.Errorf("EMQX 连接断开: %v", err)
	})

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		logx.Must(token.Error())
	}

	logx.Infof("event-dispatcher started, broker=%s, topic=%s, kafka=%s",
		c.EMQXBroker, c.EMQXTopic, c.KafkaBrokers)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	client.Disconnect(250)
	logx.Info("event-dispatcher shutting down")
}
