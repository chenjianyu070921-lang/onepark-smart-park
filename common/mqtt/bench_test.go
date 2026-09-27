package mqtt

import (
	"context"
	"io"
	"log/slog"
	"testing"

	server "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

// BenchmarkCmdDownTopic 基准指令下行 topic 拼接(纯字符串格式化, 下行发布热路径的局部开销, 作为对照基线).
func BenchmarkCmdDownTopic(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CmdDownTopic("dev-001-ABCD")
	}
}

// benchBroker 起进程内 mochi-mqtt broker(无需外部 EMQX), 返回已连接的 wrapper 客户端与清理函数.
// listeners.NewTCP(Address "127.0.0.1:0") + AddListener 在绑定后, 可经 Address() 取真实 ephemeral 端口.
func benchBroker(b *testing.B) (*Client, func()) {
	b.Helper()
	tcpL := listeners.NewTCP(listeners.Config{ID: "bench", Address: "127.0.0.1:0"})
	srv := server.New(&server.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	// 进程内 broker 用于压测, 注册 allow-all 鉴权 hook 放行所有连接与主题(真实 EMQX 的 ACL 不在此体现).
	if err := srv.AddHook(&auth.AllowHook{}, nil); err != nil {
		b.Fatalf("注册鉴权 hook 失败: %v", err)
	}
	if err := srv.AddListener(tcpL); err != nil {
		b.Fatalf("broker 监听失败: %v", err)
	}
	go func() { _ = srv.Serve() }()

	cli, err := NewClient(Conf{Broker: "tcp://" + tcpL.Address(), ClientId: "bench-pub"})
	if err != nil {
		_ = srv.Close()
		b.Fatalf("连接进程内 broker 失败: %v", err)
	}
	cleanup := func() {
		cli.Close()
		_ = srv.Close()
	}
	return cli, cleanup
}

// BenchmarkPublish 基准 wrapper 真实发布往返: 经 paho 向进程内 broker 发 QoS1 消息并等待 PUBACK.
// 覆盖"服务 -> MQTT"下行热路径的真实网络栈(serialization / ACK), 而非仅字符串拼接.
func BenchmarkPublish(b *testing.B) {
	cli, cleanup := benchBroker(b)
	defer cleanup()
	ctx := context.Background()
	topic := CmdDownTopic("bench-dev")
	payload := []byte(`{"action":"set","value":1}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.Publish(ctx, topic, QoSAtLeastOnce, payload); err != nil {
			b.Fatal(err)
		}
	}
}
