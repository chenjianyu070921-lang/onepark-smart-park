// Package cron 提供 notice-service 的定时任务.
//
// 当前任务:
//   - 公告定时发布(P1): 周期扫描到达 publish_at 仍为草稿的公告, 自动置为已发布
//     并投递 notice-event(与立即发布同链路, 供 Redis PubSub 在线推送消费).
//
// 设计说明:
//   - 建单接口约定 publish_at=0 立即发布, >0 定时发布(落库为草稿), 草稿此前无人调度 → 永不发布;
//     本任务补齐该闭环.
//   - 多实例安全: 发布动作带 status=草稿 条件更新, RowsAffected==1 的实例才投递事件,
//     重复调度/多实例部署不会重复推送.
//   - 启动补偿: 服务可能停机跨过扫描窗口, 启动时先补偿执行一次, 避免到期公告一直滞留草稿.
package cron

import (
	"context"
	"encoding/json"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/notice-service/internal/model"
	"onepark/app/notice-service/internal/svc"
	"onepark/common/kafka"
)

// publishDueScanLimit 单轮最多处理的到期草稿数, 防止积压时单轮占用过久.
const publishDueScanLimit = 100

// Start 启动公告定时发布调度(先做一次启动补偿), 返回停止函数.
// 依赖 DB 未初始化时直接跳过: 调度无库可扫; Producer 未初始化时仍可发布(仅丢事件推送, 记日志).
func Start(ctx context.Context, svcCtx *svc.ServiceContext) (stop func()) {
	logger := logx.WithContext(ctx)

	if !svcCtx.Config.PublishCron.Enabled {
		logger.Infof("[cron] 公告定时发布未启用(Enabled=false), 跳过")
		return func() {}
	}
	if svcCtx.DB == nil {
		logger.Errorf("[cron] 公告定时发布依赖 DB, 未初始化, 无法启动")
		return func() {}
	}

	spec := svcCtx.Config.PublishCron.Spec
	if spec == "" {
		spec = "*/1 * * * *"
	}

	// 启动补偿: 先把停机期间到期的草稿补发.
	go func() {
		n, err := RunPublishDueOnce(ctx, svcCtx)
		if err != nil {
			logger.Errorf("[cron] 启动补偿-公告定时发布失败: %v", err)
			return
		}
		if n > 0 {
			logger.Infof("[cron] 启动补偿-公告定时发布: 已发布 %d 条", n)
		}
	}()

	c := cron.New()
	if _, err := c.AddFunc(spec, func() {
		n, err := RunPublishDueOnce(ctx, svcCtx)
		if err != nil {
			logger.Errorf("[cron] 公告定时发布调度失败: %v", err)
			return
		}
		if n > 0 {
			logger.Infof("[cron] 公告定时发布完成: 本次发布 %d 条", n)
		}
	}); err != nil {
		logger.Errorf("[cron] 注册公告定时发布任务失败: %v", err)
	}
	c.Start()

	return func() { _ = c.Stop() }
}

// RunPublishDueOnce 执行一轮到期草稿发布: 草稿 → 已发布(条件更新防重) → 投递 notice-event.
// 返回本轮实际发布的条数; 单条失败仅记日志并继续, 不阻断其余公告发布.
func RunPublishDueOnce(ctx context.Context, svcCtx *svc.ServiceContext) (int, error) {
	logger := logx.WithContext(ctx)

	var due []model.Notice
	if e := svcCtx.DB.WithContext(ctx).
		Where("status=? AND publish_at IS NOT NULL AND publish_at<=?",
			model.NoticeStatusDraft, time.Now()).
		Order("id ASC").Limit(publishDueScanLimit).
		Find(&due).Error; e != nil {
		return 0, e
	}

	published := 0
	now := time.Now()
	for i := range due {
		n := &due[i]
		// 条件更新保证多实例/重复调度下仅一个执行者完成发布(RowsAffected==1 才投递事件).
		res := svcCtx.DB.WithContext(ctx).Model(&model.Notice{}).
			Where("id=? AND status=?", n.ID, model.NoticeStatusDraft).
			Updates(map[string]interface{}{"status": model.NoticeStatusPublished, "updated_at": now})
		if res.Error != nil {
			logger.Errorf("[cron] 发布到期公告失败 noticeId=%d: %v", n.ID, res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			continue // 已被其他实例/上一轮发布, 跳过避免重复推送
		}

		// 回填模型状态: n 是 Find 读出的草稿(status=草稿), 若不回写则推给消费者的事件 status=草稿,
		// 消费端 noticeevent.go 仅推 status==2 → 定时发布实时推送被静默丢弃(审查问题5).
		n.Status = model.NoticeStatusPublished
		n.UpdatedAt = now

		// 投递 notice-event: 载荷与建单立即发布完全一致(整条公告模型), 消费端无感知差异.
		if svcCtx.Producer != nil {
			b, err := json.Marshal(n)
			if err == nil {
				if e := svcCtx.Producer.Publish(ctx, kafka.TopicNotice, []byte(n.Title), b); e != nil {
					logger.Errorf("[cron] 投递 notice-event 失败 noticeId=%d: %v", n.ID, e)
				}
			} else {
				logger.Errorf("[cron] 序列化 notice-event 失败 noticeId=%d: %v", n.ID, err)
			}
		}
		published++
		logger.Infof("[cron] 到期公告已发布: noticeId=%d title=%s", n.ID, n.Title)
	}
	return published, nil
}
