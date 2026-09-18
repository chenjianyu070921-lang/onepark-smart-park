// Package dedup 提供消费端/写入端的幂等去重能力, 供 alarm / parking / visitor / access 等服务复用.
//
// 语义契约(全仓统一, 不得各服务自定一套):
//   - 去重组件不可用(Redis 故障)**必须返回 error**, 由调用方按可重试错误处理并重投消息,
//     严禁降级放行 —— 放行一条重复消息的代价(重复告警/重复计费/重复开门)远大于积压。
//   - 幂等键必须带 TTL, 否则键永久驻留会把 Redis 慢慢写满.
//   - 幂等键的语义必须收敛为"存在 == 该动作确实成功执行过". 若在动作**成功之前**就预占键
//     (如远程开门为防止并发重复下发而先占位), 则动作失败/被拒时**必须 Release**,
//     否则重试会拿到"假成功"(详见 Deduper.Release 注释).
//
// 三层去重中的 L1(本包)与 L2(冷却)见下; L3 是各服务库表上的唯一索引兜底, 由业务侧自行建。
package dedup

import (
	"context"
	"time"

	"onepark/common/redisx"
)

// Deduper 幂等去重器: 同一 key 首次调用返回 false, 重复调用返回 true.
type Deduper interface {
	// Seen 判定 key 是否已处理过. Redis 不可用时返回 error, 调用方不得跳过去重.
	Seen(ctx context.Context, key string) (bool, error)
	// Release 释放 key, 使同一 key 的请求可被重新处理.
	//
	// 与 Seen 配对使用, 用于"已预占但动作并未成功"的场景 —— 例如远程开门命令下发失败/超时,
	// 或请求被鉴权拒绝(根本没下发命令). 不释放的后果不是"重复开", 而是更隐蔽的
	// **假成功**: 调用方带同一幂等键重试会命中 Seen 直接拿到成功响应, 而动作其实一次都没发生.
	// 释放后 Seen 的语义收敛为"key 存在 == 该动作确实成功执行过".
	//
	// 参考 alarm 死信重放前清 L1 键(同类需求), 区别是把"清键"纳入组件契约,
	// 而不是让各服务自己裸操作 Redis 客户端.
	Release(ctx context.Context, key string) error
}

// redisDeduper 基于 go-redis SetNX 的实现, TTL 决定幂等窗口.
type redisDeduper struct {
	rdb *redisx.Client
	ttl time.Duration
}

// NewRedisDeduper 构造基于 Redis 的幂等去重器, ttl 为幂等窗口(建议 >= 消息最大重投周期).
func NewRedisDeduper(rdb *redisx.Client, ttl time.Duration) Deduper {
	return &redisDeduper{rdb: rdb, ttl: ttl}
}

func (d *redisDeduper) Seen(ctx context.Context, key string) (bool, error) {
	// SetNX: 写入成功(false)表示该消息首次处理; 写入失败(true)表示已处理过.
	ok, err := d.rdb.SetNX(ctx, key, "1", d.ttl).Result()
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (d *redisDeduper) Release(ctx context.Context, key string) error {
	// Del 的"键不存在"结果(0)不算失败: 目标状态(键已不存在)已经达成.
	return d.rdb.Del(ctx, key).Err()
}
