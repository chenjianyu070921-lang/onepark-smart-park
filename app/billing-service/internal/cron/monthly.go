package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/billing-service/internal/ecode"
	"onepark/app/billing-service/internal/logic"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
	"onepark/common/errorx"
)

const (
	monthlyLockPrefix = "m5:billing:lock:monthly:"
	monthlyLockTTL    = 2 * time.Hour
)

// releaseLockScript 仅当锁的值仍是自己的 token 时才删除(与 leasing 防重锁一致).
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// RunMonthlyBillingOnce 对指定账期(默认上个月)内有实际用量的区域自动出账。
// periodOverride 为空表示上个月; 返回本次成功出账的区域数。
//
// 三层防重复设计(与 leasing 一致):
//  1. Redis SET NX 锁(按账期粒度) —— 挡住多实例/重复调度;
//  2. generate 内部 FindBillByPeriod 唯一账期校验 —— 库层兜底不重复扣钱;
//  3. 单区域出账失败(重复/无用量/无规则)按"跳过"处理, 不中断整体出账。
func RunMonthlyBillingOnce(ctx context.Context, svcCtx *svc.ServiceContext, periodOverride string) (int64, error) {
	db := svcCtx.DB
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	tenantID := uint64(svcCtx.Config.DefaultTenantId)
	if tenantID == 0 {
		tenantID = 1
	}

	// 账期: 默认上个月(刚结束的账期), 便于月初自动出上月账
	period := periodOverride
	if period == "" {
		period = time.Now().AddDate(0, -1, 0).Format("2006-01")
	}
	start, end, ok := logic.ParsePeriod(period)
	if !ok {
		return 0, fmt.Errorf("账期格式非法: %s", period)
	}

	token, err := newLockToken()
	if err != nil {
		return 0, fmt.Errorf("生成锁标识失败: %w", err)
	}
	lockKey := monthlyLockPrefix + start.Format("200601")
	locked, err := svcCtx.Redis.SetNX(ctx, lockKey, token, monthlyLockTTL).Result()
	if err != nil {
		return 0, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !locked {
		return 0, nil
	}
	defer func() {
		_, _ = releaseLockScript.Run(ctx, svcCtx.Redis, []string{lockKey}, token).Result()
	}()

	logger := logx.WithContext(ctx)

	// 取本账期内有能耗读数的区域(能耗按区域维度, 租户由 DefaultTenantId 兜底)
	var zones []string
	if err := db.WithContext(ctx).
		Table("energy_reading").
		Where("reported_at >= ? AND reported_at < ?", start, end).
		Pluck("DISTINCT zone_id", &zones).Error; err != nil {
		return 0, fmt.Errorf("查询待出账区域失败: %w", err)
	}

	l := logic.NewBillGenerateLogic(ctx, svcCtx)
	var billed int64
	for _, z := range zones {
		resp, err := l.Generate(tenantID, &types.BillGenerateRequest{ZoneId: z, Period: period, RuleId: 0})
		if err != nil {
			if isSkipErr(err) {
				logger.Infof("[cron] 区域 %s 跳过出账: %v", z, err)
			} else {
				logger.Errorf("[cron] 区域 %s 出账失败: %v", z, err)
			}
			continue
		}
		billed++
		logger.Infof("[cron] 月度出账成功: zone=%s period=%s billNo=%s amount=%.2f", z, period, resp.BillNo, resp.Amount)
	}
	return billed, nil
}

// isSkipErr 判断出账错误是否可安全跳过(幂等/无数据类, 不构成失败).
func isSkipErr(err error) bool {
	ce, ok := err.(*errorx.CodeError)
	if !ok {
		return false
	}
	switch ce.Code {
	case ecode.ErrBillExists, ecode.ErrNoUsage, ecode.ErrNoRuleMatch:
		return true
	}
	return false
}

func newLockToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
