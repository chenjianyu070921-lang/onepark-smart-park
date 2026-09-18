package svc

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"onepark/app/video-service/internal/model"
	"onepark/common/kafka"

	kafkago "github.com/segmentio/kafka-go"
)

// deviceMessage M1 设备遥测消息体(与 app/event-dispatcher 的 dispatch.Message 对齐).
// 只声明心跳判定需要的字段, 未声明字段由 json 忽略 —— 上游加字段不会把这里打挂.
type deviceMessage struct {
	DeviceID   string `json:"device_id"`
	DeviceType string `json:"device_type"`
	EventType  string `json:"event_type"`
}

// StartHeartbeatConsumer 启动摄像头心跳消费者, 驱动 camera 的在线状态与 last_heartbeat_at.
//
// 为什么必须补这一层: 建表脚本里 last_heartbeat_at 的注释是"由设备心跳事件更新",
// 但此前没有任何消费端, 该列恒为 NULL、status 恒为初始的 0(离线),
// #51 取流因此永远返回"设备离线" —— 地址簿的在线状态是死的。
//
// 启动门禁与 alarm 一致: 缺 DB(无法落状态)或缺 broker(无法消费)都不启动, 不留半条链路.
func (s *ServiceContext) StartHeartbeatConsumer(ctx context.Context) {
	if s.Cameras == nil {
		log.Printf("[warn] video-service mysql not ready, skip camera heartbeat consumer")
		return
	}
	brokers := unresolvedToEmpty(s.Config.Kafka.Brokers)
	if brokers == "" {
		log.Printf("[warn] video-service kafka brokers empty, skip camera heartbeat consumer")
		return
	}
	go func() {
		consumer := kafka.NewConsumer(brokers, kafka.TopicDeviceTelemetry, kafka.GroupVideo)
		defer func() {
			if err := consumer.Close(); err != nil {
				log.Printf("[error] video-service close kafka consumer failed: %v", err)
			}
		}()
		log.Printf("[info] video-service start consume topic=%s group=%s", kafka.TopicDeviceTelemetry, kafka.GroupVideo)
		if err := consumer.Consume(ctx, s.handleDeviceMessage); err != nil && ctx.Err() == nil {
			log.Printf("[error] video-service heartbeat consumer exited: %v", err)
		}
	}()
}

// handleDeviceMessage 处理一条设备遥测: 命中心跳白名单的设备类型才登记心跳.
func (s *ServiceContext) handleDeviceMessage(ctx context.Context, msg kafkago.Message) error {
	var m deviceMessage
	if err := json.Unmarshal(msg.Value, &m); err != nil {
		return s.dropMessage(ctx, msg, "", err)
	}
	if m.DeviceID == "" {
		return s.dropMessage(ctx, msg, "", errors.New("missing device_id"))
	}
	if !s.Config.Heartbeat.DeviceTypeAllowed(m.DeviceType) {
		return nil
	}
	return s.onHeartbeat(ctx, m.DeviceID)
}

// dropMessage 丢弃一条坏消息并尽量留下台账.
//
// 为什么始终返回 nil(提交位移): 坏消息重试多少次结果都一样, 返回 error 会让单条毒丸卡死整个分区,
// 比丢一条心跳严重得多 —— 这个取舍没有变, 变的是"丢了之后还能不能查到"。
// 之前只有一行日志, 上游一旦改报文格式, 在线状态会静默停更且毫无痕迹; 现在补台账可追溯。
//
// 台账写入失败仍返回 nil: 心跳过期即无意义(有 StartOfflineSweeper 兜底自动置离线),
// 为一条过期心跳卡住分区不划算 —— 这点刻意与 alarm_dlq 不同(告警宁积压不误报)。
func (s *ServiceContext) dropMessage(ctx context.Context, msg kafkago.Message, deviceID string, cause error) error {
	log.Printf("[error] video-service malformed device message offset=%d err=%v payload=%s",
		msg.Offset, cause, msg.Value)
	if s.DeadLetters == nil {
		return nil
	}
	entry := &model.HeartbeatDLQ{
		Topic:       msg.Topic,
		PartitionNo: msg.Partition,
		MsgOffset:   msg.Offset,
		DeviceID:    deviceID,
		Payload:     truncateForLedger(string(msg.Value), 65535),
		ErrorMsg:    truncateForLedger(cause.Error(), 512),
		CreatedAt:   time.Now(),
	}
	if err := s.DeadLetters.Create(ctx, entry); err != nil {
		// 入账失败只留日志: 上面已权衡过, 不为台账拖住整个分区.
		log.Printf("[error] video-service write heartbeat dead letter failed offset=%d err=%v", msg.Offset, err)
	}
	return nil
}

// truncateForLedger 截断超长文本, 避免 payload/error_msg 超列宽导致整条台账写不进去.
func truncateForLedger(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// onHeartbeat 登记一次心跳. 返回 error 表示可重试(存储故障), 由消费端重投.
func (s *ServiceContext) onHeartbeat(ctx context.Context, deviceID string) error {
	at := time.Now()
	matched, err := s.Cameras.TouchHeartbeat(ctx, deviceID, at)
	if err != nil {
		log.Printf("[error] video-service touch heartbeat failed device_id=%s err=%v", deviceID, err)
		return err
	}
	switch {
	case matched == 0:
		// 设备遥测里存在未登记的摄像头: 说明地址簿漏登记, 属于"该在册却不在册", 必须留痕.
		log.Printf("[info] video-service heartbeat from unregistered camera device_id=%s", deviceID)
	case matched > 1:
		// 同一 device_id 跨租户重复: 无法归属, 绝不猜测写入(见 model.TouchHeartbeat 注释).
		log.Printf("[warn] video-service ambiguous camera device_id=%s matched=%d, skip heartbeat",
			deviceID, matched)
	default:
		// 唯一命中: 同步刷新在线缓存, 让列表接口不必等离线扫描就能看到新的在线状态.
		//
		// 失败只记日志、**不回错**: 缓存是加速层, 为它重投心跳会让 Redis 抖动卡住整个分区;
		// MySQL 侧状态已经落库, 最坏结果只是实时性退回扫描粒度(60s)。
		if s.StatusCache != nil {
			if err := s.StatusCache.Mark(ctx, deviceID, at); err != nil {
				log.Printf("[error] video-service mark heartbeat cache failed device_id=%s err=%v", deviceID, err)
			}
		}
	}
	return nil
}

// StartOfflineSweeper 周期把超时未心跳的在线摄像头置为离线.
//
// 只更新心跳会让 status 单调变成 1(在线)后永不回落 —— 摄像头掉线了地址簿仍显示在线,
// 与"没心跳"是同一个错误的两个方向。故必须有对侧的扫描。
func (s *ServiceContext) StartOfflineSweeper(ctx context.Context) {
	if s.Cameras == nil {
		log.Printf("[warn] video-service mysql not ready, skip camera offline sweeper")
		return
	}
	interval := time.Duration(s.Config.Heartbeat.ScanIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	offlineAfter := time.Duration(s.Config.Heartbeat.OfflineAfterSeconds) * time.Second
	if offlineAfter <= 0 {
		offlineAfter = 180 * time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("[info] video-service camera offline sweeper started interval=%s offline_after=%s",
			interval, offlineAfter)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := s.Cameras.MarkOffline(ctx, time.Now().Add(-offlineAfter))
				if err != nil {
					log.Printf("[error] video-service mark camera offline failed: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("[info] video-service marked %d camera(s) offline (no heartbeat within %s)", n, offlineAfter)
				}
			}
		}
	}()
}
