package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
	"onepark/app/workorder-service/internal/svc"
)

// 分布式锁: 同一实例组只有一台执行, 防止多实例重复扫描与重复升级.
// 释放必须 Lua 比对持有者, 不能直接 DEL —— 否则会把别人刚抢到的锁删掉.
// (与 leasing-service 的防重锁模式保持一致)
const (
	escalationLockPrefix = "m5:workorder:lock:escalate"
	escalationLockTTL    = 20 * time.Minute
)

// releaseLockScript 仅当锁的值仍是自己的 token 时才删除.
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// RunEscalationOnce 把"超过 PendingTimeoutHours 小时仍处于活跃态且非紧急"的工单升级为紧急优先级,
// 并返回本次升级数量.
//
// 三层防重复设计(与 leasing 一致):
//  1. Redis SET NX 锁 —— 挡住绝大多数并发;
//  2. 更新条件带 status IN(活跃态) AND priority<>紧急 AND version=旧值 —— 库层兜底, 锁失效也不会重复升级;
//  3. 升级流水仅在 RowsAffected>0 时写 —— 保证一张工单只留一条升级流水.
func RunEscalationOnce(ctx context.Context, svcCtx *svc.ServiceContext) (int64, error) {
	db := svcCtx.DB
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	cfg := svcCtx.Config.EscalationCron
	timeout := cfg.PendingTimeoutHours
	if timeout <= 0 {
		timeout = 24
	}

	token, err := newLockToken()
	if err != nil {
		return 0, fmt.Errorf("生成锁标识失败: %w", err)
	}
	ok, err := svcCtx.Redis.SetNX(ctx, escalationLockPrefix, token, escalationLockTTL).Result()
	if err != nil {
		return 0, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !ok {
		// 未抢到锁说明已有实例在执行/执行过, 属正常情况, 返回 0 不算错
		return 0, nil
	}
	defer func() {
		_, _ = releaseLockScript.Run(ctx, svcCtx.Redis, []string{escalationLockPrefix}, token).Result()
	}()

	logger := logx.WithContext(ctx)
	threshold := time.Now().Add(-time.Duration(timeout) * time.Hour)
	// 注意: 必须用 []int64 而非 []int8 —— gorm 对 []int8 切片的 IN 展开不可靠,
	// 会静默匹配 0 行(无报错), 导致升级"看起来没执行"。
	openStatuses := []int64{int64(state.StatusPendingDispatch), int64(state.StatusProcessing), int64(state.StatusPendingVerify)}

	var orders []model.WorkOrder
	if err := db.WithContext(ctx).
		Where("status IN ? AND priority <> ? AND created_at < ?", openStatuses, model.PriorityUrgent, threshold).
		Find(&orders).Error; err != nil {
		return 0, fmt.Errorf("查询待升级工单失败: %w", err)
	}

	var escalated int64
	for i := range orders {
		o := &orders[i]
		now := time.Now()
		// 状态/版本/优先级条件更新: 天然幂等, 期间被人工处理(改派/流转/关闭)则跳过
		res := db.WithContext(ctx).Model(&model.WorkOrder{}).
			Where("id = ? AND version = ? AND status IN ? AND priority <> ?", o.ID, o.Version, openStatuses, model.PriorityUrgent).
			Updates(map[string]interface{}{
				"priority":   model.PriorityUrgent,
				"version":    gorm.Expr("version+1"),
				"updated_at": now,
			})
		if res.Error != nil {
			return escalated, fmt.Errorf("超时升级失败: orderId=%d, %w", o.ID, res.Error)
		}
		if res.RowsAffected == 0 {
			continue
		}

		// 升级流水: 仅 RowsAffected>0 时写, 保证一张工单只留一条升级审计
		flow := &model.WorkOrderFlow{
			WorkOrderID: o.ID,
			FromStatus:  o.Status,
			ToStatus:    o.Status,
			Action:      "escalate",
			OperatorID:  0, // 0 = 系统操作
			Remark:      fmt.Sprintf("超时升级: 工单活跃超过 %d 小时未处理, 优先级提升为紧急", timeout),
		}
		flow.TenantID = o.TenantID
		flow.CreatedAt = now
		flow.UpdatedAt = now
		if err := db.WithContext(ctx).Create(flow).Error; err != nil {
			// 审计失败不回滚主流程, 但必须留痕
			logger.Errorf("[cron] 写工单升级流水失败: orderId=%d, err=%v", o.ID, err)
		}
		escalated++
	}

	return escalated, nil
}

// newLockToken 生成锁持有者标识.
func newLockToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
