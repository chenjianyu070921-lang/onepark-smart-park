// Package cron 提供 dispatch 的定时任务.
//
// 当前任务: **指派超时重派** —— 工单被指派后, 若在 model.AssignExpireWindow 内没人接单,
// 由本任务改派他人; 无人可派或重派次数达上限时, 释放回「待指派」交人工。
//
// 为什么必须有它: 指派时写了 assign_expire_at, 但没有消费方时这个字段等于不存在 ——
// 工单会永久卡在「已指派」, 既不出现在任何待办列表里, 也不被统计口径捞到。
//
// 为什么需要启动补偿: 服务停机期间超时的工单没人扫, 启动时补一次, 避免停机越久积压越多。
package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

const (
	// reassignLockKey 分布式锁: 同一实例组只有一台执行。
	// 不加锁的后果很具体 —— 两个实例同时扫到同一张超时工单, 会把它派给两个不同的人。
	reassignLockKey = "m5:dispatch:lock:reassign"

	// reassignLockTTL 锁 TTL 略小于默认扫描周期, 保证下一轮还能拿到锁;
	// 即使本轮异常退出, 锁也会自动过期, 不会把任务永久锁死。
	reassignLockTTL = 50 * time.Second

	// defaultIntervalSec 扫描周期兜底值(配置缺失或非法时使用)。
	defaultIntervalSec int64 = 60
)

// releaseLockScript 仅当锁的值仍是自己的 token 时才删除.
// 直接 DEL 会把别人刚抢到的锁删掉, 导致两个实例同时执行。
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// Start 启动定时任务(先做一次启动补偿), 返回停止函数.
//
// intervalSec / maxReassign 由调用方从配置传入, 本包不依赖 config,
// 与 leasing 的 cron 保持一致的风格。
func Start(ctx context.Context, db *gormx.DB, rdb *redisx.Client, intervalSec, maxReassign int64) (stop func()) {
	logger := logx.WithContext(ctx)
	if intervalSec <= 0 {
		intervalSec = defaultIntervalSec
	}

	// tag 区分"启动补偿"与"定时触发", 便于排查
	run := func(tag string) {
		res, err := RunReassignOnce(ctx, db, rdb, maxReassign)
		if err != nil {
			logger.Errorf("[cron] %s-超时重派执行失败: %v", tag, err)
			return
		}
		if res.Reassigned > 0 || res.Released > 0 {
			logger.Infof("[cron] %s-超时重派完成: 改派 %d 张, 释放回待指派 %d 张",
				tag, res.Reassigned, res.Released)
		}
	}

	// 启动补偿: 把停机期间累积的超时工单先处理掉
	go run("启动补偿")

	c := cron.New()
	if _, err := c.AddFunc(fmt.Sprintf("@every %ds", intervalSec), func() { run("定时任务") }); err != nil {
		// 表达式由常量拼出, 正常不会失败; 走到了说明代码有问题, 必须暴露而不是静默
		logger.Errorf("[cron] 注册超时重派任务失败: %v", err)
	}
	c.Start()

	return func() { _ = c.Stop() }
}

// newLockToken 生成锁持有者标识.
func newLockToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
