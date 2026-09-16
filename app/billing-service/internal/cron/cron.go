// Package cron 提供 billing-service 的定时任务.
//
// 当前任务:
//   - 月度自动出账: 每月 1 日对上个月内有实际用量的区域自动出账。
//
// 为什么需要启动补偿: 服务可能停机跨过月初执行窗口(如节假日关机),
// 启动时先补偿执行一次, 避免漏出上月账单。
package cron

import (
	"context"

	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/billing-service/internal/svc"
)

// monthlySpec 默认每月 1 日 02:02 执行(可被配置覆盖).
const monthlySpec = "0 2 1 * *"

// Start 启动全部定时任务(先做一次启动补偿), 返回停止函数.
func Start(ctx context.Context, svcCtx *svc.ServiceContext) (stop func()) {
	cfg := svcCtx.Config.MonthlyCron
	logger := logx.WithContext(ctx)

	if !cfg.Enabled {
		logger.Infof("[cron] 月度自动出账未启用(Enabled=false), 跳过")
		return func() {}
	}
	if svcCtx.DB == nil || svcCtx.Redis == nil {
		logger.Errorf("[cron] 月度自动出账依赖 DB/Redis, 未初始化, 无法启动")
		return func() {}
	}

	spec := cfg.Spec
	if spec == "" {
		spec = monthlySpec
	}

	// 启动补偿: 先把欠的账补上
	go func() {
		n, err := RunMonthlyBillingOnce(ctx, svcCtx, "")
		if err != nil {
			logger.Errorf("[cron] 启动补偿-月度自动出账失败: %v", err)
			return
		}
		if n > 0 {
			logger.Infof("[cron] 启动补偿-月度自动出账: 已出账 %d 个区域", n)
		}
	}()

	c := cron.New()
	if _, err := c.AddFunc(spec, func() {
		n, err := RunMonthlyBillingOnce(ctx, svcCtx, "")
		if err != nil {
			logger.Errorf("[cron] 月度自动出账定时任务失败: %v", err)
			return
		}
		logger.Infof("[cron] 月度自动出账定时任务完成: 本次出账 %d 个区域", n)
	}); err != nil {
		logger.Errorf("[cron] 注册定时任务失败: %v", err)
	}
	c.Start()

	return func() { _ = c.Stop() }
}
