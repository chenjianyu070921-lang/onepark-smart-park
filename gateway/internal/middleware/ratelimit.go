package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"onepark/common/errorx"
	"onepark/common/redisx"
	"onepark/common/response"
)

// tokenBucketScript 令牌桶限流 Lua 脚本: 基于剩余令牌与上次补充时间原子判定.
// KEYS[1]=限流键; ARGV[1]=当前秒级时间戳; ARGV[2]=补充速率(令牌/秒); ARGV[3]=桶容量.
// 返回 1 表示放行, 0 表示拒绝.
const tokenBucketScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local data = redis.call('HMGET', key, 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then tokens = burst end
if ts == nil then ts = now end
local delta = tokens + (now - ts) * rate
if delta > burst then delta = burst end
if delta >= 1 then
  redis.call('HSET', key, 'tokens', delta - 1, 'ts', now)
  redis.call('EXPIRE', key, math.ceil(burst/rate) + 2)
  return 1
end
return 0
`

// RateLimit 基于 Redis 令牌桶的限流 middleware.
// rdb 为 nil(未配置 Redis) 或容量 <=0 时自动放行; Redis 命令异常时亦放行,
// 避免限流组件故障导致全站不可用(优雅降级).
func RateLimit(rdb *redisx.Client, capacity int64, ratePerSec float64) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if rdb == nil || capacity <= 0 {
				next(w, r)
				return
			}
			key := "gw:ratelimit:" + clientIP(r)
			now := float64(time.Now().UnixNano()) / 1e9
			res, err := rdb.Eval(context.Background(), tokenBucketScript, []string{key}, now, ratePerSec, capacity).Int64()
			if err != nil {
				// Redis 异常: 降级放行
				next(w, r)
				return
			}
			if res != 1 {
				response.Fail(w, errorx.NewError(errorx.ErrRateLimited, "请求过于频繁,请稍后再试"))
				return
			}
			next(w, r)
		}
	}
}

// clientIP 从 X-Forwarded-For / X-Real-IP / RemoteAddr 提取客户端 IP.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
