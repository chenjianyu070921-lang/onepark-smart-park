// Package mqtt 提供 EMQX/MQTT 客户端封装, 供设备指令下行与多协议网关复用.
// 约定: event-dispatcher 负责上行订阅(MQTT -> Kafka), 本包负责下行发布(服务 -> MQTT).
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Conf MQTT 连接配置, 与 event-dispatcher 保持同一套环境变量命名.
type Conf struct {
	Broker   string `json:",env=EMQX_BROKER,default=tcp://localhost:1883"`
	ClientId string `json:",env=EMQX_CLIENT,default=onepark-mqtt-client"`
	Username string `json:",env=EMQX_USERNAME,optional"`
	Password string `json:",env=EMQX_PASSWORD,optional"`
}

// 下发 QoS 与连接超时.
const (
	QoSAtLeastOnce byte = 1
	connectTimeout      = 5 * time.Second
	publishTimeout      = 3 * time.Second
)

// 下行 topic 约定: 服务 -> 设备
const (
	// TopicCmdDownFmt 指令下发 topic: onepark/cmd/{deviceId}/down
	TopicCmdDownFmt = "onepark/cmd/%s/down"
)

// Client MQTT 客户端.
type Client struct {
	client mqtt.Client
}

// NewClient 建立 MQTT 连接, 失败返回错误由调用方决定是否降级.
func NewClient(c Conf) (*Client, error) {
	if c.Broker == "" {
		return nil, errors.New("缺少 MQTT broker 配置")
	}
	if c.ClientId == "" {
		c.ClientId = "onepark-mqtt-client"
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.Broker)
	opts.SetClientID(c.ClientId)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(time.Second)
	if c.Username != "" {
		opts.SetUsername(c.Username)
		opts.SetPassword(c.Password)
	}

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(connectTimeout) {
		return nil, fmt.Errorf("连接 MQTT 超时: broker=%s", c.Broker)
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("连接 MQTT 失败: broker=%s, err=%w", c.Broker, err)
	}
	return &Client{client: client}, nil
}

// Publish 向指定 topic 发布消息, 等待 broker 确认.
func (c *Client) Publish(_ context.Context, topic string, qos byte, payload []byte) error {
	if c == nil || c.client == nil {
		return errors.New("MQTT 客户端未初始化")
	}
	token := c.client.Publish(topic, qos, false, payload)
	if !token.WaitTimeout(publishTimeout) {
		return fmt.Errorf("发布超时: topic=%s", topic)
	}
	return token.Error()
}

// Connected 返回当前连接状态.
func (c *Client) Connected() bool {
	return c != nil && c.client != nil && c.client.IsConnected()
}

// Close 断开连接.
func (c *Client) Close() {
	if c != nil && c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(250)
	}
}

// CmdDownTopic 生成指令下发 topic.
func CmdDownTopic(deviceID string) string {
	return fmt.Sprintf(TopicCmdDownFmt, deviceID)
}
