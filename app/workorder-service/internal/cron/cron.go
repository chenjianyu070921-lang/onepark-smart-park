// Package cron 提供 workorder-service 的定时任务.
//
// 当前任务:
//   - 工单超时升级: 每 30 分钟把超过阈值仍活跃且非紧急的工单自动升级为紧急。
//
// 为什么需要启动补偿: 服务可能停机跨过扫描窗口, 启动时先补偿执行一次,
// 避免超时工单一直挂在普通优先级, 漏掉 SLA 催办。
package cron

import (
	"context"

	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/workorder-service/internal/svc"
)

// escalationSpec 默认每 30 分钟执行(可被配置覆盖).
const escalationSpec = "*/30 * * * *"

// Start 启动全部定时任务(先做一次启动补偿), 返回停止函数.
func Start(ctx context.Context, svcCtx *svc.ServiceContext) (stop func()) {
	cfg := svcCtx.Config.EscalationCron
	logger := logx.WithContext(ctx)

	if !cfg.Enabled {
		logger.Infof("[cron] 工单超时升级未启用(Enabled=false), 跳过")
		return func() {}
	}
	if svcCtx.DB == nil || svcCtx.Redis == nil {
		logger.Errorf("[cron] 工单超时升级依赖 DB/Redis, 未初始化, 无法启动")
		return func() {}
	}

	spec := cfg.Spec
	if spec == "" {
		spec = escalationSpec
	}

	// 启动补偿: 不管现在几点, 先把欠的账补上
	go func() {
		n, err := RunEscalationOnce(ctx, svcCtx)
		if err != nil {
			logger.Errorf("[cron] 启动补偿-工单超时升级失败: %v", err)
			return
		}
		if n > 0 {
			logger.Infof("[cron] 启动补偿-工单超时升级: 已升级 %d 张工单", n)
		}
	}()

	c := cron.New()
	if _, err := c.AddFunc(spec, func() {
		n, err := RunEscalationOnce(ctx, svcCtx)
		if err != nil {
			logger.Errorf("[cron] 工单超时升级定时任务失败: %v", err)
			return
		}
		logger.Infof("[cron] 工单超时升级定时任务完成: 本次升级 %d 张", n)
	}); err != nil {
		logger.Errorf("[cron] 注册定时任务失败: %v", err)
	}
	c.Start()

	return func() { _ = c.Stop() }
}
