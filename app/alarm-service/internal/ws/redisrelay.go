package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	goredis "github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"
)

// redisPubSub 抽象 go-redis 的 Pub/Sub 能力.
// 收窄到这五个方法而不是直接用 *goredis.Client: 上层逻辑可在无 Redis 环境单测.
type redisPubSub interface {
	Publish(ctx context.Context, channel string, message interface{}) *goredis.IntCmd
	Subscribe(ctx context.Context, channels ...string) *goredis.PubSub
}

// redisRelay 基于 Redis Pub/Sub 的跨实例广播(docs/m3/09 §4 方案B).
//
// 选它而非 Kafka 广播 topic 的理由(文档结论): 零新组件(Redis 已在用)、满足实时性。
// 代价是 Pub/Sub **无持久化** —— 断线期间的消息会丢, 由前端重连后拉活跃告警列表补偿。
type redisRelay struct {
	client  redisPubSub
	channel string

	mu sync.Mutex
	ps *goredis.PubSub // 惰性创建; Receive 出错时置 nil 以便重建
}

// NewRedisRelay 构造 Redis 广播中继.
func NewRedisRelay(client redisPubSub, channel string) Relay {
	return &redisRelay{client: client, channel: channel}
}

func (r *redisRelay) Publish(ctx context.Context, msg *BusMessage) error {
	raw, err := msg.Encode()
	if err != nil {
		return err
	}
	return r.client.Publish(ctx, r.channel, raw).Err()
}

// Receive 阻塞读取一条广播; 订阅在首次调用时惰性建立.
//
// 订阅中断(网络抖动 / 频道关闭)时关闭旧订阅并返回错误, 由 relayLoop 退避后重新调用,
// 下次调用会重建订阅 —— 比依赖 go-redis 内部重连更可靠, 因为订阅状态一旦失效无法自愈.
func (r *redisRelay) Receive(ctx context.Context) (*BusMessage, error) {
	ps, err := r.subscription(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case raw, ok := <-ps.Channel():
		if !ok {
			r.dropSubscription()
			return nil, errors.New("ws: redis pubsub channel closed")
		}
		var msg BusMessage
		if err := json.Unmarshal([]byte(raw.Payload), &msg); err != nil {
			// 总线上混入脏消息不该阻断整个订阅: 记日志丢弃, 返回 (nil, nil) 让循环继续.
			logx.Errorf("ws relay drop malformed bus message channel=%s err=%v", r.channel, err)
			return nil, nil
		}
		return &msg, nil
	}
}

// subscription 返回已确认建立的订阅, 必要时新建.
// 用 Receive 显式确认订阅在服务端已建立 —— 否则紧随其后的第一条 PUBLISH 会因
// 订阅尚未就绪而丢失(SUBSCRIBE 是异步的, 不确认就返回会踩这个竞态).
func (r *redisRelay) subscription(ctx context.Context) (*goredis.PubSub, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ps != nil {
		return r.ps, nil
	}
	ps := r.client.Subscribe(ctx, r.channel)
	if _, err := ps.Receive(ctx); err != nil {
		_ = ps.Close()
		return nil, err
	}
	r.ps = ps
	return ps, nil
}

// dropSubscription 关闭并清空当前订阅.
func (r *redisRelay) dropSubscription() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ps != nil {
		_ = r.ps.Close()
		r.ps = nil
	}
}

func (r *redisRelay) Close() error {
	r.dropSubscription()
	return nil
}
