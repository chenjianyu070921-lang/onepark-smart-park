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

	// lockTTLMargin 锁 TTL 相对扫描周期预留的余量(见 ReassignLockTTL)。
	lockTTLMargin = 10 * time.Second

	// defaultIntervalSec 扫描周期兜底值(配置缺失或非法时使用)。
	defaultIntervalSec int64 = 60
)

// ReassignLockTTL 由扫描周期推导分布式锁的 TTL: **interval - 10s, 且不小于 interval/2**。
//
// 为什么不能让 TTL 与周期脱钩(原先是硬编码 50s):
// 正常路径下锁是**会被主动释放**的 —— RunReassignOnce 用
// `defer releaseLockScript(仅当值仍是自己 token 时删除)` 收尾。TTL 只兜两种异常:
//
//	① 进程在扫描中途崩溃/被 kill -> defer 没跑到, 锁靠 TTL 自动过期;
//	② 释放时 ctx 已取消 -> Lua 删除静默失败(错误被刻意忽略), 同样靠 TTL。
//
// 所以 TTL 的取舍是:
//
//	**太长**: 异常退出后锁迟迟不释放, 后续几轮扫描被无声跳过(工单一直卡在"已指派");
//	**太短**: 单轮扫描还没跑完锁就过期, 另一个实例会并发进来扫同一批(同一张单被派给两个人)。
//
// 硬编码 50s 只对"默认 60s 周期"成立 —— 把 IntervalSec 调到 300,
// 锁在 50s 就过期, 而这一轮的扫描窗口有 300s: 恰恰是"太短"那一侧。
// 故 TTL 必须由周期推导, 且保留 10s 余量让下一轮能正常抢锁。
func ReassignLockTTL(intervalSec int64) time.Duration {
	if intervalSec <= 0 {
		intervalSec = defaultIntervalSec
	}
	interval := time.Duration(intervalSec) * time.Second

	ttl := interval - lockTTLMargin
	if min := interval / 2; ttl < min {
		// 周期很短(<=20s)时余量会吃掉大半甚至变成负数, 兜一个下界
		ttl = min
	}
	return ttl
}

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

	// 锁 TTL 必须跟着扫描周期推导, 不能硬编码 —— 理由见 ReassignLockTTL 注释
	lockTTL := ReassignLockTTL(intervalSec)

	// tag 区分"启动补偿"与"定时触发", 便于排查
	run := func(tag string) {
		res, err := RunReassignOnce(ctx, db, rdb, maxReassign, lockTTL)
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
