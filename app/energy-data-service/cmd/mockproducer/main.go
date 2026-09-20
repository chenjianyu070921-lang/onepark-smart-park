// 联调工具: 按 M1 的真实格式往 Kafka 发消息, 验证 M4 消费端能正确收下并入库
//
// 用法: go run ./cmd/mockproducer
//
// 发四种消息, 覆盖真实场景:
//  1. 电表遥测(带 zone_id)      → 应该入库
//  2. 电表遥测(不带 zone_id)     → 应该入库, 区域走 device 表/默认区域兜底
//  3. 地磁/门禁事件(无 energy)   → 应该静默跳过, 不打错误日志
//  4. 脏数据(无设备号/负数/坏JSON) → 应该丢弃并告警
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"onepark/app/energy-data-service/internal/mq"
	"onepark/common/kafka"
)

func main() {
	broker := flag.String("broker", "127.0.0.1:9092", "kafka 地址")
	// 默认就用公共常量, 和 M1 发布端保持一致
	topic := flag.String("topic", kafka.TopicDeviceTelemetry, "topic 名")
	flag.Parse()

	w := &kafkago.Writer{
		Addr:         kafkago.TCP(*broker),
		Topic:        *topic,
		Balancer:     &kafkago.LeastBytes{},
		RequiredAcks: kafkago.RequireOne,
		Async:        false,
	}
	defer w.Close()

	now := time.Now()
	power := 3.5

	// payload 里带电量, 用 json.RawMessage 模拟 M1 原样透传设备报文
	energy := func(kwh float64, zoneID string) json.RawMessage {
		m := map[string]interface{}{"energy_kwh": kwh, "power_kw": power}
		if zoneID != "" {
			m["zone_id"] = zoneID
		}
		b, _ := json.Marshal(m)
		return b
	}
	// 地磁事件: 没有 energy_kwh, M4 应该跳过
	geomagnetic := func() json.RawMessage {
		b, _ := json.Marshal(map[string]interface{}{"occupied": true, "battery": 87})
		return b
	}

	var msgs []kafkago.Message
	add := func(ev mq.DeviceEvent) {
		b, _ := json.Marshal(ev)
		msgs = append(msgs, kafkago.Message{Value: b})
	}

	// 1. 8 条正常电表数据: 两台设备, 读数递增, 带 zone_id
	for i := 0; i < 8; i++ {
		add(mq.DeviceEvent{
			RequestID:  fmt.Sprintf("req-%d", i),
			DeviceID:   fmt.Sprintf("MOCK-%02d", i%2+1),
			DeviceType: "meter",
			EventType:  "telemetry",
			OccurredAt: now.Add(-time.Duration(8-i) * time.Minute).Unix(),
			Payload:    energy(5000+float64(i)*2.5, "C栋"),
			Source:     "mqtt",
		})
	}
	// 2. 1 条不带 zone_id 的, 验证区域兜底(device 表没数据时应归到"未分配")
	add(mq.DeviceEvent{
		RequestID:  "req-nozone",
		DeviceID:   "MOCK-03",
		DeviceType: "meter",
		EventType:  "telemetry",
		OccurredAt: now.Unix(),
		Payload:    energy(7777, ""),
		Source:     "mqtt",
	})
	// 3. 2 条非能耗事件: 地磁/门禁, 应静默跳过
	add(mq.DeviceEvent{
		RequestID: "req-geo", DeviceID: "SENSOR-01", DeviceType: "geomagnetic",
		EventType: "parking", OccurredAt: now.Unix(), Payload: geomagnetic(), Source: "mqtt",
	})
	add(mq.DeviceEvent{
		RequestID: "req-door", DeviceID: "DOOR-01", DeviceType: "access",
		EventType: "door_open", OccurredAt: now.Unix(), Payload: geomagnetic(), Source: "mqtt",
	})
	// 4. 脏数据: 没设备号 / 负读数 / 不是 JSON
	add(mq.DeviceEvent{DeviceID: "", DeviceType: "meter", EventType: "telemetry",
		OccurredAt: now.Unix(), Payload: energy(8888, "C栋"), Source: "mqtt"})
	add(mq.DeviceEvent{DeviceID: "MOCK-BAD", DeviceType: "meter", EventType: "telemetry",
		OccurredAt: now.Unix(), Payload: energy(-1, "C栋"), Source: "mqtt"})
	msgs = append(msgs, kafkago.Message{Value: []byte("这不是JSON")})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := w.WriteMessages(ctx, msgs...); err != nil {
		fmt.Println("发送失败(Kafka 起来了吗? docker start onepark-kafka-dev):", err)
		return
	}
	fmt.Printf("已向 topic=%s 发送 %d 条: 8 正常(带区域) + 1 无区域 + 2 非能耗事件 + 3 脏数据\n",
		*topic, len(msgs))
	fmt.Println("预期: 入库 9 条(C栋 8 条 + 未分配 1 条), 非能耗事件静默跳过, 脏数据 3 条告警")
}
