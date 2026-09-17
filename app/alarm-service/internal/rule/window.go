package rule

import (
	"context"
	"fmt"
	"time"

	"onepark/common/redisx"

	"github.com/redis/go-redis/v9"
)

// windowLua 滑动窗口计数脚本(docs/m3/08 §3):
// 清理窗口外成员 → 写入当前成员 → 计数 → 达到阈值则清空窗口并返回计数(>0 表示触发).
// 返回 0 表示未触发. 用 member=requestId 天然去重, 同一消息重复投递不会重复计数.
const windowLua = `
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, ARGV[1] - ARGV[2])
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[3])
local cnt = redis.call('ZCARD', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[5])
if cnt >= tonumber(ARGV[4]) then
  redis.call('DEL', KEYS[1])
  return cnt
end
return 0
`

// WindowCounter 滑动窗口计数器: 记录一次事件并返回窗口内是否达到触发阈值.
type WindowCounter interface {
	// Add 将 member 计入窗口, 返回 (是否触发, 窗口内计数).
	// Redis 不可用时必须返回 error, 由调用方按可重试错误处理(宁积压不误报, docs/m3/08 §5).
	Add(ctx context.Context, key string, member string, window time.Duration, threshold int, ttl time.Duration) (bool, int64, error)
}

// redisWindow 基于 Redis Lua 的滑动窗口实现.
type redisWindow struct {
	script *redis.Script
	rdb    *redisx.Client
}

// NewRedisWindow 构造基于 Redis 的滑动窗口计数器.
func NewRedisWindow(rdb *redisx.Client) WindowCounter {
	return &redisWindow{script: redis.NewScript(windowLua), rdb: rdb}
}

func (w *redisWindow) Add(ctx context.Context, key string, member string,
	window time.Duration, threshold int, ttl time.Duration) (bool, int64, error) {
	nowMs := time.Now().UnixMilli()
	res, err := w.script.Run(ctx, w.rdb, []string{key},
		nowMs, window.Milliseconds(), member, threshold, int64(ttl.Seconds())).Result()
	if err != nil {
		return false, 0, fmt.Errorf("rule: sliding window eval failed: %w", err)
	}
	// go-redis 对 Lua 返回整数统一为 int64; 0 表示未达阈值, >0 为窗口内计数.
	cnt, ok := res.(int64)
	if !ok {
		return false, 0, fmt.Errorf("rule: sliding window 返回类型异常 %T", res)
	}
	return cnt > 0, cnt, nil
}

// WindowKey 生成滑动窗口键: alarm:window:{ruleID}:{deviceID}(docs/m3/08 §1).
func WindowKey(ruleID int64, deviceID string) string {
	return fmt.Sprintf("alarm:window:%d:%s", ruleID, deviceID)
}
