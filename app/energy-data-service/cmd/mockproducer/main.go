// 模拟 M1 设备上报: 往 Kafka 发一批遥测数据(含 2 条脏数据用于验证清洗逻辑)
// 用法: go run ./cmd/mockproducer （可选 -broker 127.0.0.1:9092 -topic onepark.device.telemetry）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"onepark/app/energy-data-service/internal/mq"
)

func main() {
	broker := flag.String("broker", "127.0.0.1:9092", "kafka 地址")
	topic := flag.String("topic", "onepark.device.telemetry", "topic 名")
	flag.Parse()

	w := &kafka.Writer{
		Addr:         kafka.TCP(*broker),
		Topic:        *topic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
		Async:        false,
	}
	defer w.Close()

	now := time.Now()
	powerOK, powerBad := 3.5, 0.0

	// 8 条正常数据: 两台设备, 每条间隔 1 分钟, 读数递增
	var msgs []kafka.Message
	for i := 0; i < 8; i++ {
		t := mq.Telemetry{
			DeviceID:   fmt.Sprintf("MOCK-%02d", i%2+1),
			ZoneID:     "C栋",
			EnergyKwh:  5000 + float64(i)*2.5,
			PowerKw:    &powerOK,
			ReportedAt: now.Add(-time.Duration(8-i) * time.Minute).Format("2006-01-02 15:04:05"),
		}
		b, _ := json.Marshal(t)
		msgs = append(msgs, kafka.Message{Value: b})
	}
	// 脏数据1: 没有设备号
	b1, _ := json.Marshal(mq.Telemetry{ZoneID: "C栋", EnergyKwh: 8888, ReportedAt: now.Format("2006-01-02 15:04:05")})
	msgs = append(msgs, kafka.Message{Value: b1})
	// 脏数据2: 电表读数是负数
	b2, _ := json.Marshal(mq.Telemetry{DeviceID: "MOCK-BAD", ZoneID: "C栋", EnergyKwh: -1, PowerKw: &powerBad, ReportedAt: now.Format("2006-01-02 15:04:05")})
	msgs = append(msgs, kafka.Message{Value: b2})
	// 脏数据3: JSON 都不合法
	msgs = append(msgs, kafka.Message{Value: []byte("这不是JSON")})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := w.WriteMessages(ctx, msgs...)
	if err != nil {
		fmt.Println("发送失败(服务/Kafka 起来了吗?):", err)
		return
	}
	fmt.Printf("已向 topic=%s 发送 %d 条消息(8 正常 + 3 脏数据)\n", *topic, len(msgs))
}
