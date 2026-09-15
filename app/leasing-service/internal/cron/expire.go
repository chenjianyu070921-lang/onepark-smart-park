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

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/state"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// 分布式锁: 同一实例组只有一台执行, 防止多实例重复扫描与重复写审计.
// 释放必须 Lua 比对持有者, 不能直接 DEL —— 否则会把别人刚抢到的锁删掉.
const (
	expireLockPrefix = "m5:lease:lock:expire:"
	expireLockTTL    = 10 * time.Minute
)

// releaseLockScript 仅当锁的值仍是自己的 token 时才删除.
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// RunExpireOnce 把终止日已过的「生效中」合同转为「已到期」, 返回本次处理数量.
//
// 三层防重复设计(锁只是第一层, 锁会丢):
//  1. Redis SET NX 锁 —— 挡住绝大多数并发
//  2. 更新条件带 status=生效中 AND version=旧值 —— 数据库层兜底, 锁失效也不会重复转移状态
//  3. 审计流水仅在 RowsAffected>0 时写 —— 保证一份合同只留一条 expire 流水
func RunExpireOnce(ctx context.Context, db *gormx.DB, rdb *redisx.Client) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}

	token, err := newLockToken()
	if err != nil {
		return 0, fmt.Errorf("生成锁标识失败: %w", err)
	}
	lockKey := expireLockPrefix + time.Now().Format("20060102")

	// 未抢到锁说明今天已有实例执行过(或正在执行), 属正常情况, 返回 0 不算错
	ok, err := rdb.SetNX(ctx, lockKey, token, expireLockTTL).Result()
	if err != nil {
		return 0, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !ok {
		return 0, nil
	}
	defer func() {
		_, _ = releaseLockScript.Run(ctx, rdb, []string{lockKey}, token).Result()
	}()

	var contracts []model.LeaseContract
	if err := db.WithContext(ctx).
		Where("status = ? AND end_date < ?", model.StatusActive, todayBegin()).
		Find(&contracts).Error; err != nil {
		return 0, fmt.Errorf("查询待到期合同失败: %w", err)
	}

	var expired int64
	for i := range contracts {
		c := &contracts[i]
		// 逐条更新(每日到期量很小, 无需批量): 带状态与版本条件, 天然幂等
		res := db.WithContext(ctx).Model(&model.LeaseContract{}).
			Where("id = ? AND version = ? AND status = ?", c.Id, c.Version, model.StatusActive).
			Updates(map[string]interface{}{
				"status":  model.StatusExpired,
				"version": gorm.Expr("version+1"),
			})
		if res.Error != nil {
			return expired, fmt.Errorf("合同转到期失败: contractId=%d, %w", c.Id, res.Error)
		}
		if res.RowsAffected == 0 {
			continue // 期间被人工处理过(如续签/终止), 跳过
		}

		if err := db.WithContext(ctx).Create(&model.LeaseContractStatusLog{
			ContractId: c.Id,
			FromStatus: model.StatusActive,
			ToStatus:   model.StatusExpired,
			Action:     state.ActionExpire,
			Reason:     "租期届满, 系统自动到期",
			OperatorId: 0, // 0 = 系统操作
		}).Error; err != nil {
			// 审计失败不回滚主流程, 但必须留痕
			logx.WithContext(ctx).Errorf("[cron] 写合同审计失败: contractId=%d, err=%v", c.Id, err)
		}
		expired++
	}

	return expired, nil
}

// todayBegin 今天 0 点. 终止日等于今天的合同不算到期 —— 租期应覆盖终止日全天.
func todayBegin() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
}

// newLockToken 生成锁持有者标识.
func newLockToken() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
