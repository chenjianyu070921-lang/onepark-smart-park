package svc

import (
	"context"
	"strconv"
	"time"

	"github.com/zeromicro/go-zero/core/stores/redis"
)

// heartbeatKeyPrefix 摄像头心跳缓存键前缀, 完整键形如 video:camera:heartbeat:{deviceID}.
const heartbeatKeyPrefix = "video:camera:heartbeat:"

// defaultOfflineAfterSec 缓存 TTL 兜底值, 与 HeartbeatConf.OfflineAfterSeconds 默认值一致.
const defaultOfflineAfterSec = 180

// StatusCacheStore 摄像头在线状态缓存的读写面.
//
// 抽成接口是为了让 logic 层单测能注入替身: 否则"Redis 命中在线"这条分支
// 只能依赖真实 Redis 才能覆盖, 单测环境跑不到, 也就等于没测过。
//
// 注意: 字段值为 nil 时调用方必须先回退 MySQL 状态, **不要**直接调用 —— nil 接口调用会 panic。
type StatusCacheStore interface {
	// Mark 登记一次心跳时间戳.
	Mark(ctx context.Context, deviceID string, at time.Time) error
	// Online 返回设备是否在离线阈值内上报过心跳, 以及最近心跳时间.
	Online(ctx context.Context, deviceID string) (bool, time.Time)
}

// StatusCache 摄像头在线状态缓存(docs/m3/04 #50「在线状态检测 + Redis 缓存」).
//
// 定位: MySQL 的 camera.status 仍是**事实来源**, Redis 只是"最近一次心跳"的加速层.
// 它解决一个具体问题: 离线判定靠周期扫描(默认 60s), 扫描前 MySQL 里的状态是滞后的,
// 而心跳本身是实时的 —— 把心跳时间戳放进带 TTL 的 key 里, 列表接口就能立刻反映在线状态,
// 且 TTL 到期自动失效, 不需要额外的清理任务。
//
// 所有方法在缓存为 nil / 未配置 / Redis 故障时都返回零值且不报错:
// 它是可选的加速层, 不能因为 Redis 抖动就让"查看摄像头列表"失败,
// 更不能让"登记一次心跳"失败(那会导致心跳被反复重投, 卡住整个 Kafka 分区)。
type StatusCache struct {
	rdb    *redis.Redis
	ttlSec int
}

// NewStatusCache 构造在线状态缓存. ttl 为心跳标记存活时长, 应与离线判定阈值一致:
// 短于阈值会误判在线设备离线, 长于阈值会让离线状态迟迟不生效。
func NewStatusCache(rdb *redis.Redis, ttl time.Duration) *StatusCache {
	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = defaultOfflineAfterSec
	}
	return &StatusCache{rdb: rdb, ttlSec: ttlSec}
}

// Mark 登记一次心跳时间戳, TTL 到期即视为离线.
func (c *StatusCache) Mark(ctx context.Context, deviceID string, at time.Time) error {
	if c == nil || c.rdb == nil || deviceID == "" {
		return nil
	}
	return c.rdb.SetexCtx(ctx, heartbeatKeyPrefix+deviceID,
		strconv.FormatInt(at.UnixMilli(), 10), c.ttlSec)
}

// Online 查询设备是否在离线阈值内上报过心跳, 同时返回最近心跳时间.
//
// 返回 false 有两种含义(未命中 / Redis 不可用), 调用方都应回退到 MySQL 中的 status:
// 缓存是"锦上添花", 它说不出来的时候不能让查询整体失败。
func (c *StatusCache) Online(ctx context.Context, deviceID string) (bool, time.Time) {
	if c == nil || c.rdb == nil || deviceID == "" {
		return false, time.Time{}
	}
	raw, err := c.rdb.GetCtx(ctx, heartbeatKeyPrefix+deviceID)
	if err != nil {
		// redis.Nil(未命中)与连接故障在此不做区分: 对调用方而言结果都是"缓存给不出答案".
		return false, time.Time{}
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		// 脏值按未命中处理, 不 panic 也不返回零值时间当作"在线".
		return false, time.Time{}
	}
	return true, time.UnixMilli(ms)
}
