// Package cron 实现调度工单的后台定时任务.
//
// ExpireScanner 扫描「已指派但超过 assign_expire_at 仍未接单」的工单,
// 退回「待指派」等待重新指派, 兑现 convert.go assignExpireWindow 注释承诺的重派闭环.
//
// 多实例安全: 每条工单的回退都带乐观锁条件(version + status), 并发扫描时
// 仅一个实例更新成功(RowsAffected==1), 其余自动跳过, 无需分布式锁.
package cron

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/common/gormx"
)

const (
	// scanInterval 扫描周期.
	scanInterval = 30 * time.Second
	// scanBatch 单轮最多处理的工单数, 防止积压时单轮耗时过长.
	scanBatch = 100
)

// ExpireScanner 指派超时扫描器.
type ExpireScanner struct {
	logx.Logger
	db *gormx.DB
}

// NewExpireScanner 构造扫描器; db 为 nil 时 Start 直接跳过.
func NewExpireScanner(db *gormx.DB) *ExpireScanner {
	return &ExpireScanner{
		Logger: logx.WithContext(context.Background()),
		db:     db,
	}
}

// Start 阻塞运行扫描循环, 应在独立 goroutine 中调用; ctx 取消即退出.
func (s *ExpireScanner) Start(ctx context.Context) {
	if s.db == nil {
		s.Info("[cron] db 未初始化, 指派超时扫描不启动")
		return
	}

	s.Infof("[cron] 指派超时扫描启动: interval=%s, batch=%d", scanInterval, scanBatch)
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.Info("[cron] 指派超时扫描已停止")
			return
		case <-ticker.C:
			reset, err := s.resetExpired(ctx)
			if err != nil {
				s.Errorf("[cron] 指派超时扫描失败: %v", err)
				continue
			}
			if reset > 0 {
				s.Infof("[cron] 本轮回退超时工单 %d 张", reset)
			}
		}
	}
}

// resetExpired 回退一轮超时工单, 返回成功回退的数量.
func (s *ExpireScanner) resetExpired(ctx context.Context) (int, error) {
	var tasks []model.DispatchTask
	// 不带租户过滤: 超时回收是全租户的后台运维动作, 回退时保留原租户不动.
	err := s.db.WithContext(ctx).
		Where("status = ? AND assign_expire_at IS NOT NULL AND assign_expire_at < ?",
			model.StatusAssigned, time.Now()).
		Limit(scanBatch).
		Find(&tasks).Error
	if err != nil {
		return 0, err
	}

	reset := 0
	for _, t := range tasks {
		ok := false
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&model.DispatchTask{}).
				Where("id = ? AND status = ? AND version = ?", t.Id, model.StatusAssigned, t.Version).
				Updates(map[string]interface{}{
					"status":           model.StatusPendingAssign,
					"assignee_id":      0,
					"assignee_name":    "",
					"assign_expire_at": nil,
					"version":          gorm.Expr("version+1"),
				})
			if res.Error != nil {
				return res.Error
			}
			// RowsAffected==0: 已被其它实例或并发请求处理, 跳过不视为错误.
			ok = res.RowsAffected > 0
			if !ok {
				return nil
			}
			return tx.Create(&model.DispatchTaskLog{
				TenantID:   t.TenantID,
				TaskId:     t.Id,
				FromStatus: model.StatusAssigned,
				ToStatus:   model.StatusPendingAssign,
				Action:     state.ActionExpire,
				Remark:     "指派超时未接单, 自动退回待指派",
			}).Error
		})
		if err != nil {
			s.Errorf("[cron] 回退工单失败: taskId=%d, err=%v", t.Id, err)
			continue
		}
		if ok {
			reset++
		}
	}
	return reset, nil
}
