// Package lock 提供基于 Redis 的分布式锁(SET NX + 看门狗续期).
// 适用于 M2 二维码核销等需跨进程互斥的场景.
package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"onepark/common/redisx"
)

// 错误定义.
var (
	// ErrLockAcquired 表示锁已被其他持有者占用(本次获取失败).
	ErrLockAcquired = errors.New("lock: already acquired by others")
	// ErrLockNotHeld 表示解锁时锁已不属于当前持有者(可能已过期被他人获取).
	ErrLockNotHeld = errors.New("lock: lock not held by this caller")
)

// renewScript 仅当 token 匹配时续期, 防止续到他人锁上.
var renewScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
	return 0
end`)

// unlockScript 仅当 token 匹配时删除, 防止误删他人锁.
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end`)

// Locker 分布式锁管理器, 基于 redisx.Client.
type Locker struct {
	rdb *redis.Client
}

// New 创建锁管理器.
func New(rdb *redisx.Client) *Locker {
	return &Locker{rdb: rdb}
}

// Handle 一次成功获取的锁. 使用完毕必须调用 Unlock 释放.
type Handle struct {
	l      *Locker
	key    string
	token  string
	expire time.Duration

	cancel context.CancelFunc
	once   sync.Once
}

// TryLock 非阻塞获取锁: 成功返回 Handle 并启动看门狗续期;
// 已被占用返回 ErrLockAcquired; ctx 取消/超时返回 ctx.Err().
func (l *Locker) TryLock(ctx context.Context, key string, expire time.Duration) (*Handle, error) {
	if expire <= 0 {
		return nil, errors.New("lock: expire must be positive")
	}
	token := newToken()
	ok, err := l.rdb.SetNX(ctx, key, token, expire).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLockAcquired
	}
	h := &Handle{l: l, key: key, token: token, expire: expire}
	h.startWatchdog()
	return h, nil
}

// Unlock 释放锁(仅当 token 匹配). 并发调用安全(仅首次生效).
func (h *Handle) Unlock(ctx context.Context) error {
	var unlockErr error
	h.once.Do(func() {
		if h.cancel != nil {
			h.cancel() // 停止看门狗
		}
		res, err := unlockScript.Run(ctx, h.l.rdb, []string{h.key}, h.token).Int64()
		if err != nil {
			unlockErr = err
			return
		}
		if res == 0 {
			unlockErr = ErrLockNotHeld
			return
		}
	})
	return unlockErr
}

// startWatchdog 在后台按 expire/3 周期续期, 直到 Unlock 或锁丢失.
func (h *Handle) startWatchdog() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		ticker := time.NewTicker(h.expire / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
				res, err := renewScript.Run(rctx, h.l.rdb, []string{h.key}, h.token,
					int(h.expire.Milliseconds())).Int64()
				rcancel()
				// 续期失败或锁已不属于本持有者(res==0)时退出看门狗, 避免无限续期.
				if err != nil || res == 0 {
					return
				}
			}
		}
	}()
}

// newToken 生成随机防误删令牌.
func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 极罕见失败, 退化为时间兜底(仍具足够随机性).
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
