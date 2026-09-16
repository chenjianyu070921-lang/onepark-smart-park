// Package cron 提供 leasing 的定时任务.
//
// 当前任务:
//   - 合同自动到期: 每日 00:05 将终止日已过的「生效中」合同转为「已到期」。
//
// 为什么需要启动补偿: 服务可能停机跨过凌晨执行窗口(如周末关机),
// 启动时先补偿执行一次, 避免到期合同一直挂在「生效中」,
// 污染入驻率统计、到期提醒和账单生成口径。
package cron

import (
	"context"

	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// expireSpec 每日 00:05 执行.
const expireSpec = "5 0 * * *"

// Start 启动全部定时任务(先做一次启动补偿), 返回停止函数.
func Start(ctx context.Context, db *gormx.DB, rdb *redisx.Client) (stop func()) {
	logger := logx.WithContext(ctx)

	// 启动补偿: 不管现在几点, 先把欠的账补上
	go func() {
		n, err := RunExpireOnce(ctx, db, rdb)
		if err != nil {
			logger.Errorf("[cron] 启动补偿-合同到期执行失败: %v", err)
			return
		}
		if n > 0 {
			logger.Infof("[cron] 启动补偿-合同到期: 已自动到期 %d 份合同", n)
		}
	}()

	c := cron.New()
	if _, err := c.AddFunc(expireSpec, func() {
		n, err := RunExpireOnce(ctx, db, rdb)
		if err != nil {
			logger.Errorf("[cron] 合同到期定时任务失败: %v", err)
			return
		}
		logger.Infof("[cron] 合同到期定时任务执行完成: 本次自动到期 %d 份", n)
	}); err != nil {
		// 表达式是常量, 正常不会走到这里; 走到了说明代码有问题, 必须暴露
		logger.Errorf("[cron] 注册定时任务失败: %v", err)
	}
	c.Start()

	// c.Stop 返回 func() context.Context, 包装成无参停止函数
	return func() { _ = c.Stop() }
}
