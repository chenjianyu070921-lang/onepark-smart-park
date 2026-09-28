// telemetryproducer 往 Kafka(device-telemetry) 投递一条(或多条)停车地磁遥测消息,
// 供 parking-service 消费链路联调与端到端验证使用.
//
// 为什么需要它: parking 的入场/离场记录由 M1 设备遥测驱动, 但 M1(device-service / gateway)
// 常没起(联调/演示时尤其如此)。本工具让你**不依赖任何上游服务**就能造出真实数据。
//
// 用法:
//
//	go run ./app/parking-service/tools/telemetryproducer \
//	  -brokers 127.0.0.1:19092 -topic device-telemetry \
//	  -event-type entry -device-id geom-01 -tenant-id 1 -plate 苏A12345 -vehicle-type 0 -count 1
//
// ⚠️ 与 alarmproducer 同样的工程约定: 仓库根目录没有 go.mod, 工具必须落在某个 Go 模块内;
// 故放在 parking-service 模块(tools/ 子目录), 由 deploy/test/parking_kafka_e2e.ps1 包装调用。
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

// deviceTelemetry 与 app/parking-service/internal/svc/consumer.deviceTelemetry 字段一一对应。
// 这里**刻意重新声明**而不是 import: consumer 是 parking-service 的内部包,
// 一旦字段调整, 工具与消费者会一起失败, 反而看不出"契约漂移"。
// 独立声明 + 下面注释标注对齐关系, 改契约时这里必须同步改。
type deviceTelemetry struct {
	RequestID  string          `json:"request_id"`  // 幂等键(M1 侧生成); 缺失时消费端按指纹降级
	DeviceID   string          `json:"device_id"`   // 地磁/门禁设备ID
	EventType  string          `json:"event_type"`  // entry 入场 / exit 离场
	OccurredAt int64           `json:"occurred_at"` // 事件时间(秒级时间戳)
	Payload    json.RawMessage `json:"payload"`     // 业务载荷: 标准链路停车字段在 payload 内
	Source     string          `json:"source"`      // mqtt / http-fallback / tcp-gateway

	// 以下为旧扁平格式兜底字段; 标准链路应只填 payload, 这里同时回填以保证向后兼容。
	TenantID    int64  `json:"tenant_id"`
	PlateNo     string `json:"plate_no"`
	VehicleType int8   `json:"vehicle_type"`
	Event       string `json:"event"`      // 旧字段名, 兜底回退
	Timestamp   int64  `json:"timestamp"`  // 旧时间字段, 兜底回退
}

// payload 标准信封 payload 内的停车业务字段(tenant_id/plate_no/vehicle_type)。
type payload struct {
	TenantID    int64  `json:"tenant_id"`
	PlateNo     string `json:"plate_no"`
	VehicleType int8   `json:"vehicle_type"`
}

func main() {
	brokers := flag.String("brokers", "127.0.0.1:19092", "Kafka broker 地址, 多个用逗号分隔")
	topic := flag.String("topic", kafka.TopicDeviceTelemetry, "目标 topic")
	eventType := flag.String("event-type", "entry", "事件类型: entry 入场 / exit 离场")
	deviceID := flag.String("device-id", "geom-01", "地磁设备ID")
	tenantID := flag.Int64("tenant-id", 1, "园区ID(RBAC 隔离; 缺失会被消费端丢弃)")
	plateNo := flag.String("plate", "苏A12345", "车牌号")
	vehicleType := flag.Int("vehicle-type", 0, "车型(0=未知, 由月卡表自动判定; 1月卡 2临时 3VIP 4异常)")
	requestID := flag.String("request-id", "", "指定 request_id: 给了就所有消息复用同一个(用于验证幂等); 不给则每条自动生成")
	key := flag.String("key", "", "消息 key(决定落哪个分区); 默认取 plate")
	count := flag.Int("count", 1, "投递条数")
	occurredAt := flag.Int64("occurred-at", 0, "事件时间(秒级); 0=当前时间")
	intervalMS := flag.Int("interval-ms", 0, "每条之间的间隔毫秒(测并发时可用 0)")
	raw := flag.String("raw", "", "直接发送这个字面量而忽略其它参数 —— 用于构造毒消息(非法 JSON / 缺字段)")
	flag.Parse()

	if *key == "" {
		*key = *plateNo
	}

	producer := kafka.NewProducer(*brokers)
	defer func() { _ = producer.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*count)*30*time.Second+30*time.Second)
	defer cancel()

	for i := 1; i <= *count; i++ {
		now := time.Now().Unix()
		ts := *occurredAt
		if ts == 0 {
			ts = now
		}
		value, reqID := buildValue(*raw, *requestID, *deviceID, *eventType, *tenantID, *plateNo, int8(*vehicleType), ts, i)

		if err := producer.Publish(ctx, *topic, []byte(*key), value); err != nil {
			// 投递失败必须非零退出 —— 脚本靠退出码判断"投没投进去", 静默失败会让断言看起来像业务 bug。
			fmt.Fprintf(os.Stderr, "[telemetryproducer] 第 %d 条投递失败: %v\n", i, err)
			os.Exit(1)
		}
		// 打印 request_id 供脚本捕获, 后续用来查库断言/做幂等复投。
		fmt.Printf("[telemetryproducer] 已投递 %d/%d: topic=%s key=%s request_id=%s event=%s plate=%s ts=%d\n",
			i, *count, *topic, *key, reqID, *eventType, *plateNo, ts)
		if *intervalMS > 0 && i < *count {
			time.Sleep(time.Duration(*intervalMS) * time.Millisecond)
		}
	}
	fmt.Printf("[telemetryproducer] 完成: 共 %d 条 -> %s@%s\n", *count, *topic, *brokers)
}

// buildValue 组装一条消息体。raw 非空时原样返回(用于毒消息用例)。
func buildValue(raw, requestID, deviceID, eventType string, tenantID int64, plateNo string, vehicleType int8, occurredAt int64, seq int) ([]byte, string) {
	if raw != "" {
		return []byte(raw), "(raw)"
	}

	reqID := requestID
	if reqID == "" {
		// 带序号与纳秒时间戳: 同一秒内多次调用也不会撞号(撞号会被幂等索引当成重复消息)。
		reqID = fmt.Sprintf("req-park-%d-%d", time.Now().UnixNano(), seq)
	}

	p, _ := json.Marshal(payload{TenantID: tenantID, PlateNo: plateNo, VehicleType: vehicleType})

	evt := deviceTelemetry{
		RequestID:  reqID,
		DeviceID:   deviceID,
		EventType:  eventType,
		OccurredAt: occurredAt,
		Payload:    p,
		Source:     "telemetryproducer",
		// 旧扁平格式兜底字段: 与 payload 保持一致, 保证两种解析路径都拿到业务值。
		TenantID:    tenantID,
		PlateNo:     plateNo,
		VehicleType: vehicleType,
		Event:       eventType,
		Timestamp:   occurredAt,
	}
	value, err := json.Marshal(evt)
	if err != nil {
		log.Fatalf("序列化遥测消息失败: %v", err)
	}
	return value, reqID
}
