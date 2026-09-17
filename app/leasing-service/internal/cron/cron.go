// Package cron 提供 leasing 的定时任务.
//
// 当前任务(每日维护, 顺序不可颠倒):
//  1. 自动续约: 把「约定自动续约」且已过终止日的合同按原租期长度顺延
//  2. 自动到期: 把其余已过终止日的「生效中」合同转为「已到期」
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

// dailySpec 每日 00:05 执行.
const dailySpec = "5 0 * * *"

// Start 启动全部定时任务(先做一次启动补偿), 返回停止函数.
func Start(ctx context.Context, db *gormx.DB, rdb *redisx.Client) (stop func()) {
	logger := logx.WithContext(ctx)

	// tag 用于区分"启动补偿"与"定时触发", 便于日志排查
	run := func(tag string) {
		res, err := RunDailyOnce(ctx, db, rdb)
		if err != nil {
			logger.Errorf("[cron] %s-每日维护执行失败: %v", tag, err)
			return
		}
		if res.Renewed > 0 || res.Expired > 0 {
			logger.Infof("[cron] %s-每日维护完成: 自动续约 %d 份, 自动到期 %d 份",
				tag, res.Renewed, res.Expired)
		}
	}

	// 启动补偿: 不管现在几点, 先把欠的账补上
	go run("启动补偿")

	c := cron.New()
	if _, err := c.AddFunc(dailySpec, func() { run("定时任务") }); err != nil {
		// 表达式是常量, 正常不会走到这里; 走到了说明代码有问题, 必须暴露
		logger.Errorf("[cron] 注册定时任务失败: %v", err)
	}
	c.Start()

	// c.Stop 返回 func() context.Context, 包装成无参停止函数
	return func() { _ = c.Stop() }
}
