package wshub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/common/redisx"
)

// Publisher 把"本实例的广播"转给其它实例。
//
// nil 表示单实例部署: 只在本进程内投递(默认, 行为与引入扇出之前完全一致)。
type Publisher interface {
	Publish(ctx context.Context, msg []byte)
}

// envelope 扇出信封。
//
// 为什么要带 from: 订阅端会收到**自己发出去**的消息(Redis Pub/Sub 不区分来源),
// 若不丢弃, 本地客户端就会收到两份(一份来自本地投递, 一份来自订阅回放)。
// Payload 用 []byte, JSON 序列化时自动 base64, 避免二进制内容被当字符串截断。
type envelope struct {
	From    string `json:"from"`
	Payload []byte `json:"payload"`
}

// RedisFanout 基于 Redis Pub/Sub 的跨实例广播。
//
// 设计取舍: **本地优先 + 发布给其它实例**(而不是"只发 Redis、等订阅回来再投递")。
// 理由: 单实例演示时 Redis 常被关掉/连不上, 若本地投递也要经过 Redis,
// 那么 Redis 一挂**在线大屏立刻全静默**; 本地优先则最坏情况只是"跨实例不生效",
// 本实例的大屏照常刷新 —— 与本模块"逐源降级、可用性优先"的口径一致。
type RedisFanout struct {
	rdb       *redisx.Client
	channel   string
	instance  string
	published int64 // 仅用于日志/排查: 累计发布条数
}

// NewRedisFanout 创建扇出器。instanceID 为空时自动生成(主机名+纳秒), 只用于识别"是不是自己发的"。
func NewRedisFanout(rdb *redisx.Client, channel string) *RedisFanout {
	return &RedisFanout{
		rdb:      rdb,
		channel:  channel,
		instance: newInstanceID(),
	}
}

// InstanceID 本实例标识(日志用)。
func (f *RedisFanout) InstanceID() string { return f.instance }

// Publish 实现 Publisher。发布失败只记日志、不返回错误也不阻塞广播:
// 跨实例推送是"尽力而为"的增强, 不能因为它失败而拖慢本地大屏。
func (f *RedisFanout) Publish(ctx context.Context, msg []byte) {
	body, err := json.Marshal(envelope{From: f.instance, Payload: msg})
	if err != nil {
		logx.WithContext(ctx).Errorf("[ws-fanout] 信封序列化失败: %v", err)
		return
	}
	if err := f.rdb.Publish(ctx, f.channel, body).Err(); err != nil {
		logx.WithContext(ctx).Errorf("[ws-fanout] 发布失败(跨实例推送本次不生效, 本实例不受影响): %v", err)
		return
	}
	f.published++
}

// Run 订阅扇出频道并把**其它实例**的消息投递给本实例的连接; 阻塞运行, 应在独立 goroutine 中调用.
//
// 收到自己的消息必须丢弃: 本地投递已经发生过一次(见 RedisFanout 注释)。
func (f *RedisFanout) Run(ctx context.Context, hub *Hub) {
	logx.WithContext(ctx).Infof("[ws-fanout] 已启动: channel=%s instance=%s", f.channel, f.instance)

	for {
		// 外层 for: 断线重连。Redis 短暂重启或网络抖动后必须能自己恢复,
		// 否则多实例推送会永久失效却没人发现(日志里有"已启动"但那是一次性的)。
		if err := f.runOnce(ctx, hub); err != nil {
			if ctx.Err() != nil {
				logx.WithContext(ctx).Info("[ws-fanout] 已停止")
				return
			}
			logx.WithContext(ctx).Errorf("[ws-fanout] 订阅中断, 1s 后重连: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		return
	}
}

func (f *RedisFanout) runOnce(ctx context.Context, hub *Hub) error {
	sub := f.rdb.Subscribe(ctx, f.channel)
	defer func() { _ = sub.Close() }()

	// 先确认订阅真的建立, 再进入收消息循环 —— 否则"启动了但收不到"会静默存在。
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}

	ch := sub.Channel()
	var relayed, skipped int64
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("订阅通道已关闭")
			}
			var env envelope
			if err := json.Unmarshal([]byte(msg.Payload), &env); err != nil {
				logx.WithContext(ctx).Errorf("[ws-fanout] 收到无法解析的消息, 已跳过: %v", err)
				continue
			}
			if env.From == f.instance {
				skipped++ // 自己发的, 本地早就投递过了
				continue
			}
			hub.DeliverLocal(env.Payload)
			relayed++
			if relayed%100 == 0 {
				logx.WithContext(ctx).Infof("[ws-fanout] 已转发跨实例消息 %d 条(丢弃自回环 %d 条), 本机在线=%d",
					relayed, skipped, hub.Count())
			}
		}
	}
}

func newInstanceID() string {
	host, _ := os.Hostname()
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", host, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(buf))
}
