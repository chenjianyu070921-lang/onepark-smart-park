package mqtt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNewClientRequiresBroker 空 broker 必须即时失败, 不能静默建出"未连接客户端".
func TestNewClientRequiresBroker(t *testing.T) {
	client, err := NewClient(Conf{})
	if err == nil {
		t.Fatal("空 Broker 应返回错误")
	}
	if client != nil {
		t.Fatal("失败时不应返回客户端实例")
	}
	if !strings.Contains(err.Error(), "缺少 MQTT broker 配置") {
		t.Fatalf("错误信息不符: %v", err)
	}
}

// TestNewClientUnreachableBroker 不可达 broker 应在连接超时(5s)内失败, 保证调用方快速降级.
func TestNewClientUnreachableBroker(t *testing.T) {
	start := time.Now()
	client, err := NewClient(Conf{Broker: "tcp://127.0.0.1:1", ClientId: "onepark-mqtt-ut"})
	if err == nil {
		client.Close()
		t.Fatal("不可达 broker 应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("连接失败耗时 %v, 超出预期(应受 connectTimeout 约束)", elapsed)
	}
}

// TestNilClientSafety 未初始化客户端的调用必须安全返回, 不得 panic.
func TestNilClientSafety(t *testing.T) {
	var c *Client
	if c.Connected() {
		t.Fatal("nil 客户端不应报告已连接")
	}
	// Publish 内部有 c == nil 守卫, 对 nil 接收者调用是安全的.
	if err := c.Publish(context.Background(), CmdDownTopic("D1"), QoSAtLeastOnce, []byte("x")); err == nil {
		t.Fatal("nil 客户端 Publish 应返回错误")
	}
	c.Close() // 不应 panic
}

// TestCmdDownTopic 固化下行 topic 约定: onepark/cmd/{deviceId}/down.
func TestCmdDownTopic(t *testing.T) {
	if got, want := CmdDownTopic("DEV-001"), "onepark/cmd/DEV-001/down"; got != want {
		t.Fatalf("CmdDownTopic = %q, want %q", got, want)
	}
}

// TestPublishIntegrationWithBroker 真实 EMQX 连接验证(需 EMQX_BROKER).
// 未配置时自动跳过; 配置后验证"连接 -> 发布 -> 连接态 -> 关闭"全链路.
// 注意: 发布使用测试专用 topic, 不触碰 onepark/cmd/<deviceId>/down 指令通道, 避免误控真实设备.
func TestPublishIntegrationWithBroker(t *testing.T) {
	broker := os.Getenv("EMQX_BROKER")
	if broker == "" {
		t.Skip("未设置 EMQX_BROKER, 跳过真实 MQTT 连接用例")
	}

	client, err := NewClient(Conf{
		Broker:   broker,
		ClientId: "onepark-mqtt-ut",
		Username: os.Getenv("EMQX_USERNAME"),
		Password: os.Getenv("EMQX_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("连接 EMQX 失败: %v", err)
	}
	defer client.Close()

	if !client.Connected() {
		t.Fatal("连接成功后 Connected() 应为 true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Publish(ctx, "onepark/test/mqtt-wrapper-check", QoSAtLeastOnce, []byte(`{"probe":true}`)); err != nil {
		t.Fatalf("发布测试消息失败: %v", err)
	}
}
