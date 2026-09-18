// Package cron 提供 leasing 的定时任务.
//
// 每日维护(每日 00:05, 顺序不可颠倒):
//  1. 自动续约: 把「约定自动续约」且已过终止日的合同按原租期长度顺延
//  2. 自动到期: 把其余已过终止日的「生效中」合同转为「已到期」
//
// 月度出账(每月 1 号 00:10): 生成上一自然月的租金账单。
//
// 为什么需要启动补偿: 服务可能停机跨过执行窗口(如周末关机/发版宕机),
// 启动时先补偿执行一次, 避免到期合同一直挂在「生效中」
// (会污染入驻率统计、到期提醒和账单生成口径), 或整月漏出账。
package cron

import (
	"context"

	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/leasing-service/internal/svc"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// dailySpec 每日 00:05 执行(合同续约/到期).
const dailySpec = "5 0 * * *"

// monthlySpec 每月 1 号 00:10 执行(上一自然月出账).
//
// 刻意与 dailySpec 错开 5 分钟: 出账依赖"该到期的合同已经转过状态"这一结果,
// 同一时刻启动会让两者争抢连接与锁, 也让日志难以归因。
const monthlySpec = "10 0 1 * *"

// Start 启动全部定时任务(先各做一次启动补偿), 返回停止函数.
func Start(ctx context.Context, svcCtx *svc.ServiceContext) (stop func()) {
	logger := logx.WithContext(ctx)

	runDaily := func(db *gormx.DB, rdb *redisx.Client, tag string) {
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

	runBill := func(tag string) {
		res, err := RunBillOnce(ctx, svcCtx)
		if err != nil {
			// 启动补偿时另一个实例可能正持锁, 报"正在生成中"属正常, 不必当作告警
			logger.Errorf("[cron] %s-月度出账执行失败: %v", tag, err)
			return
		}
		// 仅在真有新账单时打日志: 否则每次重启都刷一行"created=0", 把真正的异常淹掉
		if res.Created > 0 {
			logger.Infof("[cron] %s-月度出账完成: period=%s, 新生成 %d 张, 幂等跳过 %d 张",
				tag, res.Period, res.Created, res.Skipped)
		}
	}

	// 启动补偿: 把停机期间欠的账补上
	go runDaily(svcCtx.DB, svcCtx.Redis, "启动补偿")
	go runBill("启动补偿")

	c := cron.New()
	if _, err := c.AddFunc(dailySpec, func() { runDaily(svcCtx.DB, svcCtx.Redis, "定时任务") }); err != nil {
		// 表达式是常量, 正常不会走到这里; 走到了说明代码有问题, 必须暴露
		logger.Errorf("[cron] 注册每日维护任务失败: %v", err)
	}
	if _, err := c.AddFunc(monthlySpec, func() { runBill("定时任务") }); err != nil {
		logger.Errorf("[cron] 注册月度出账任务失败: %v", err)
	}
	c.Start()

	// c.Stop 返回 func() context.Context, 包装成无参停止函数
	return func() { _ = c.Stop() }
}
