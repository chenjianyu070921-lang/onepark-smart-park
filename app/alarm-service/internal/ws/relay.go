package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
)

// BroadcastChannel 跨实例广播的默认频道名(docs/m3/09 §4 方案B).
const BroadcastChannel = "alarm:ws:broadcast"

// relayRetryDelay 订阅中断后的重连退避间隔.
const relayRetryDelay = time.Second

// relayPublishTimeout 单次 PUBLISH 的超时上限.
// 广播是告警落库后的旁路, 不能让 Redis 抖动长时间占住调用方 goroutine.
const relayPublishTimeout = 2 * time.Second

// BusMessage 总线上的一条广播.
//
// 必须携带 tenant_id: 客户端信封(docs/m3/09 §5)是给浏览器看的, 不含租户字段;
// 而每个实例在本地投递时必须按园区过滤, 否则会把 A 园区的告警推进 B 园区的大屏.
type BusMessage struct {
	TenantID int64           `json:"tenant_id"`
	Payload  json.RawMessage `json:"payload"`
}

// NewBusMessage 构造总线消息; payload 为已序列化的客户端信封.
func NewBusMessage(tenantID int64, payload []byte) *BusMessage {
	return &BusMessage{TenantID: tenantID, Payload: payload}
}

// Encode 序列化总线消息.
func (m *BusMessage) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// Relay 跨实例广播中继(docs/m3/09 §4 方案B: Redis Pub/Sub).
//
// 抽成纯 Go 接口(不暴露 go-redis 类型)是为了让上层逻辑能在无 Redis 的环境下单测,
// 生产实现见 redisRelay; 未注入 Relay 时 Hub 退化为单实例内存广播.
type Relay interface {
	// Publish 把消息投递到总线(所有实例, 含本实例).
	Publish(ctx context.Context, msg *BusMessage) error
	// Receive 阻塞读取下一条总线消息.
	// 返回错误表示订阅已中断, 由调用方退避后重新调用(下次调用应重建订阅).
	Receive(ctx context.Context) (*BusMessage, error)
	// Close 释放订阅资源.
	Close() error
}

// StartRelay 开启跨实例广播: 注入中继并启动订阅循环.
//
// 投递路径说明: 本实例 Push 的消息同样经由订阅回调回来, 因此本地与跨实例走
// **完全相同**的投递路径 —— 不会出现"本地直投 + 订阅回环"导致的重复推送.
func (h *Hub) StartRelay(ctx context.Context, r Relay) {
	if h == nil || r == nil {
		return
	}
	h.mu.Lock()
	h.relay = r
	h.mu.Unlock()
	go h.relayLoop(ctx, r)
}

// relayLoop 订阅总线并把消息投递给本实例的客户端.
// 任何 Receive 错误(网络抖动/订阅失效)都只退避重试, 不让循环退出 ——
// 否则该实例的客户端会静默地再也收不到推送.
func (h *Hub) relayLoop(ctx context.Context, r Relay) {
	defer func() { _ = r.Close() }()
	for {
		if ctx.Err() != nil {
			return
		}
		msg, err := r.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logx.Errorf("ws relay receive failed, resubscribe in %s: %v", relayRetryDelay, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(relayRetryDelay):
			}
			continue
		}
		// 脏消息由实现层丢弃后返回 (nil, nil).
		if msg == nil {
			continue
		}
		// 本实例无该园区的连接属正常情况(大屏连在别的实例上), 不是错误.
		h.BroadcastTo(msg.TenantID, msg.Payload)
	}
}

// relayOf 读取当前中继, 未启用时返回 nil.
func (h *Hub) relayOf() Relay {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.relay
}
