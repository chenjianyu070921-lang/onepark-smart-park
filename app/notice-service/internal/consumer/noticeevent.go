package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/notice-service/internal/config"
	"onepark/common/kafka"
	"onepark/common/redisx"

	"strings"
)

// noticeEventChannel 在线推送的 Redis PubSub 频道名(按园区隔离).
// 前端/网关长连接层订阅该频道, 收到消息即向在线用户推送公告横幅/红点.
func noticeEventChannel(tenantID int64) string {
	return fmt.Sprintf("notice-push:%d", tenantID)
}

// NoticeEvent 公告发布事件载荷: 生产侧(notice-service CreateNotice)直接序列化的
// model.Notice JSON, 本结构只声明消费侧关心的字段.
type NoticeEvent struct {
	ID        int64      `json:"id"`        // 公告ID
	TenantID  int64      `json:"tenant_id"` // 园区ID(决定推送频道)
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	Type      int8       `json:"type"`   // 1通知 2公告 3活动 4停水 5停电
	Top       int8       `json:"top"`    // 是否置顶
	Status    int8       `json:"status"` // 2=已发布(生产侧仅发布时投递, 兜底校验)
	PublishAt *time.Time `json:"publish_at"`
}

// DecodeNoticeEvent 解析一条公告发布事件.
// 非法消息返回 error, 由调用方记录后跳过(不能让坏消息卡住消费分区).
func DecodeNoticeEvent(value []byte) (NoticeEvent, error) {
	var ev NoticeEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		return NoticeEvent{}, fmt.Errorf("解析公告事件失败: %w", err)
	}
	if ev.ID <= 0 {
		return NoticeEvent{}, errors.New("公告事件缺少合法 id")
	}
	if ev.TenantID <= 0 {
		return NoticeEvent{}, errors.New("公告事件缺少 tenant_id, 无法定位推送频道")
	}
	return ev, nil
}

// NoticeEventHandler 公告发布事件处理器: 收到事件后经 Redis PubSub 推送给在线用户.
//
// 渠道语义(对齐 notice.api 头注"已读统计(Redis PubSub 推送)"):
//   - 公告本身已落 notice 表(生产侧), 消费侧不重复落库 —— 离线用户上线后按公告列表拉取;
//   - 在线用户的"实时触达"由本处理器完成: PUBLISH notice-push:{tenant_id},
//     长连接层/前端订阅该频道即时弹窗; 未读计数走 notice_read + unread-count 接口.
//
// 幂等: 推送是"通知性质"的重复消费无副作用(在线端收到重复消息幂等刷新),
// Kafka 至少一次投递下允许重复推送, 不做额外去重存储.
type NoticeEventHandler struct {
	logx.Logger
	redis *redisx.Client
}

// NewNoticeEventHandler 构造公告推送处理器.
func NewNoticeEventHandler(rdb *redisx.Client) *NoticeEventHandler {
	return &NoticeEventHandler{
		Logger: logx.WithContext(context.Background()),
		redis:  rdb,
	}
}

// Handle 处理一条公告发布事件: 校验 → PubSub 推送在线用户.
// 返回 nil 表示"本条已处理完(含坏消息丢弃)", 消费循环据此提交位移.
func (h *NoticeEventHandler) Handle(ctx context.Context, value []byte) error {
	ev, err := DecodeNoticeEvent(value)
	if err != nil {
		// 坏消息必须"记录后跳过": 若返回 error, 位移不提交, 整个分区会被这一条卡死.
		h.Errorf("[consumer] 丢弃非法公告事件: %v", err)
		return nil
	}
	// 仅已发布(2)或已撤回(3)触发推送: 撤回时前端据 status 实时下架横幅(审查问题6, 原仅放行2导致撤回不实时下架).
	if ev.Status != 2 && ev.Status != 3 {
		h.Infof("[consumer] 公告非已发布/已撤回状态, 跳过推送: id=%d status=%d", ev.ID, ev.Status)
		return nil
	}
	if h.redis == nil {
		return fmt.Errorf("redis 未初始化, 无法推送公告事件: noticeId=%d", ev.ID)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"event":       "notice_published",
		"id":          ev.ID,
		"tenant_id":   ev.TenantID,
		"title":       ev.Title,
		"content":     ev.Content,
		"notice_type": ev.Type,
		"top":         ev.Top,
		"publish_at":  publishAtUnix(ev.PublishAt),
	})
	if err := h.redis.Publish(ctx, noticeEventChannel(ev.TenantID), payload).Err(); err != nil {
		return fmt.Errorf("推送公告事件失败(noticeId=%d): %w", ev.ID, err) // 返回 error 等待重试, 推送不可静默丢
	}

	h.Infof("[consumer] 公告推送完成: noticeId=%d channel=%s title=%s", ev.ID, noticeEventChannel(ev.TenantID), ev.Title)
	return nil
}

// publishAtUnix 发布时间转秒级时间戳, nil 返回 0.
func publishAtUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}

// NoticeEventRunner 公告推送消费者运行器: 装配 consumer+handler, 在独立 goroutine 常驻消费.
type NoticeEventRunner struct {
	logx.Logger
	handler *NoticeEventHandler
	topic   string // 消费的公告事件主题, 供启动日志展示
	group   string // 消费组, 断线重建消费者时复用
	enabled bool   // 配置开关: 共享 broker 上误开会真实推送, 默认关闭
	brokers string // broker 列表原样保留, 用于空配置守卫与重建
}

// NewNoticeEventRunner 根据配置装配消费者; Enabled=false 时返回空跑实例(Start 直接跳过).
// noticeEventTopic 返回公告事件消费主题: 配置未显式指定时回落为 notice-event,
// 避免复用 KafkaConf 默认 workorder-event 导致订阅错主题、推送静默失效(审查问题7).
func noticeEventTopic(t string) string {
	if strings.TrimSpace(t) == "" {
		return kafka.TopicNotice
	}
	return t
}

func NewNoticeEventRunner(cfg config.KafkaConf, rdb *redisx.Client) *NoticeEventRunner {
	return &NoticeEventRunner{
		Logger:  logx.WithContext(context.Background()),
		handler: NewNoticeEventHandler(rdb),
		topic:   noticeEventTopic(cfg.Topic),
		group:   cfg.Group,
		enabled: cfg.Enabled,
		brokers: cfg.Brokers,
	}
}

// Start 启动消费循环(阻塞), 必须在独立 goroutine 中调用; ctx 取消即退出.
// 带断线重连(与 parking 消费端同范式): broker 瞬时故障/重启导致 Consume 返回错误时,
// 间隔 3 秒重建消费者继续消费; 避免一次网络抖动让公告推送永久停摆.
func (r *NoticeEventRunner) Start(ctx context.Context) {
	if !r.enabled {
		r.Infof("[consumer] 公告推送消费者未启用(Enabled=false), 跳过消费")
		return
	}
	if r.brokers == "" {
		// Brokers 为空时 Reader 会在拨号阶段反复报错, 显式跳过并留痕.
		r.Infof("[consumer] 公告推送消费者未启用(KAFKA_BROKERS 未配置), 跳过消费")
		return
	}
	r.Infof("[consumer] 公告推送消费者启动: topic=%s", r.topic)

	for {
		consumer := kafka.NewConsumer(r.brokers, r.topic, r.group)
		// Consume 阻塞循环: handler 返回 nil 才提交位移, 返回 error 保留消息等待重试.
		err := consumer.Consume(ctx, func(ctx context.Context, msg kafkago.Message) error {
			return r.handler.Handle(ctx, msg.Value)
		})
		_ = consumer.Close() // 每轮重建前释放旧 reader, 防止连接泄漏
		if ctx.Err() != nil {
			return
		}
		r.Errorf("[consumer] 公告推送消费循环异常退出: %v, 3s 后重建消费者", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
