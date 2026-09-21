// alarmproducer 往 Kafka 投递一条(或多条)格式正确的告警消息, 供 M5 联调与演示使用.
//
// 为什么需要它: M5 的告警自动建单依赖 M1 event-dispatcher 投递消息,
// 但 M1 常没起(联调/演示时尤其如此)。本工具让你**不依赖任何上游服务**就能造出真实数据。
//
// 用法:
//
//	go run ./app/dispatch-service/tools/alarmproducer \
//	  -brokers 127.0.0.1:19092 -topic alarm-event \
//	  -event-type fire -device-id SMOKE-001 -zone A-1F-101 -count 1
//
// ⚠️ 关于落位: 计划书原定放在 deploy/test/alarm_producer.go, 但**仓库根目录没有 go.mod**,
// deploy/ 不属于任何一个 Go 模块 —— 放在那里 `go run` 无法解析 onepark/common 的导入。
// 故落在 dispatch-service 模块内(tools/ 子目录), 由 deploy/test/alarm_producer.ps1 包装调用。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"onepark/common/kafka"
)

// alarmEvent 与 app/dispatch-service/internal/consumer.AlarmEvent 字段一一对应。
// 这里**刻意重新声明**而不是 import: consumer 是 dispatch-service 的内部包,
// 工具放同模块内本可 import, 但那样会把工具的编译绑死在业务包上 ——
// 一旦字段调整, 工具与消费者会一起失败, 反而看不出"契约漂移"。
// 独立声明 + 下面注释标注对齐关系, 改契约时这里必须同步改。
type alarmEvent struct {
	RequestID  string          `json:"request_id"`
	DeviceID   string          `json:"device_id"`
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	OccurredAt int64           `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"`
}

// payload 与 consumer.payloadZone 对齐: zone_code 优先, location 兜底.
type payload struct {
	ZoneCode string `json:"zone_code,omitempty"`
	Location string `json:"location,omitempty"`
}

func main() {
	brokers := flag.String("brokers", "127.0.0.1:19092", "Kafka broker 地址, 多个用逗号分隔")
	topic := flag.String("topic", kafka.TopicAlarm, "目标 topic")
	eventType := flag.String("event-type", "fire", "告警类型: fire/smoke/intrusion/door_force/fault/offline_alert(未知类型按普通优先级建单)")
	deviceID := flag.String("device-id", "", "设备编号(必填, 消费者会校验)")
	deviceType := flag.String("device-type", "smoke", "设备类型")
	zone := flag.String("zone", "", "区域编码, 会写进 payload.zone_code(决定自动指派能否就近选人)")
	requestID := flag.String("request-id", "", "指定 request_id: 给了就所有消息复用同一个(用于验证 uk_alarm_id 幂等); 不给则每条自动生成")
	key := flag.String("key", "", "消息 key(决定落哪个分区); 默认取 device-id")
	count := flag.Int("count", 1, "投递条数")
	intervalMS := flag.Int("interval-ms", 0, "每条之间的间隔毫秒(测并发时可用 0)")
	raw := flag.String("raw", "", "直接发送这个字面量而忽略其它参数 —— 用于构造毒消息(非法 JSON / 缺字段)")
	flag.Parse()

	if *deviceID == "" && *raw == "" {
		log.Fatal("必须提供 -device-id(或用 -raw 直接投递原始内容)")
	}
	if *key == "" {
		*key = *deviceID
	}

	producer := kafka.NewProducer(*brokers)
	defer func() { _ = producer.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*count)*30*time.Second+30*time.Second)
	defer cancel()

	for i := 1; i <= *count; i++ {
		value, reqID := buildValue(*raw, *requestID, *deviceID, *deviceType, *eventType, *zone, i)

		if err := producer.Publish(ctx, *topic, []byte(*key), value); err != nil {
			// 投递失败必须非零退出 —— 脚本靠退出码判断"投没投进去", 静默失败会让断言看起来像业务 bug。
			fmt.Fprintf(os.Stderr, "[alarmproducer] 第 %d 条投递失败: %v\n", i, err)
			os.Exit(1)
		}
		// 打印 request_id 供脚本捕获, 后续用来查库断言/做幂等复投。
		fmt.Printf("[alarmproducer] 已投递 %d/%d: topic=%s key=%s request_id=%s event_type=%s zone=%s\n",
			i, *count, *topic, *key, reqID, *eventType, *zone)

		if *intervalMS > 0 && i < *count {
			time.Sleep(time.Duration(*intervalMS) * time.Millisecond)
		}
	}
	fmt.Printf("[alarmproducer] 完成: 共 %d 条 -> %s@%s\n", *count, *topic, *brokers)
}

// buildValue 组装一条消息体。raw 非空时原样返回(用于毒消息用例)。
func buildValue(raw, requestID, deviceID, deviceType, eventType, zone string, seq int) ([]byte, string) {
	if raw != "" {
		return []byte(raw), "(raw)"
	}

	reqID := requestID
	if reqID == "" {
		// 带序号与纳秒时间戳: 同一秒内多次调用也不会撞号(撞号会被幂等索引当成重复告警)。
		reqID = fmt.Sprintf("req-m5-%d-%d", time.Now().UnixNano(), seq)
	}

	var p []byte
	if zone != "" {
		p, _ = json.Marshal(payload{ZoneCode: zone, Location: zone})
	}

	evt := alarmEvent{
		RequestID:  reqID,
		DeviceID:   deviceID,
		DeviceType: deviceType,
		EventType:  eventType,
		OccurredAt: time.Now().Unix(),
		Payload:    p,
		Source:     "alarmproducer",
	}
	value, err := json.Marshal(evt)
	if err != nil {
		log.Fatalf("序列化告警消息失败: %v", err)
	}
	return value, reqID
}
