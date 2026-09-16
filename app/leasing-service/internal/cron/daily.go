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
	dailyLockPrefix = "m5:lease:lock:daily:"
	dailyLockTTL    = 10 * time.Minute
)

// maxRenewYears 自动续约可顺延的年数上限, 防止脏数据造出超长租期.
const maxRenewYears = 30

// dateLayout 合同日期格式。与 package lease 的同名常量保持一致的对外口径
// (cron 与本服务的 logic 层不互相依赖, 故各自持有一份)。
const dateLayout = "2006-01-02"

// releaseLockScript 仅当锁的值仍是自己的 token 时才删除.
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

// DailyResult 一次每日维护的结果.
type DailyResult struct {
	Renewed int64 // 按合同条款自动顺延的份数
	Expired int64 // 转为「已到期」的份数
}

// RunDailyOnce 执行每日维护: **先自动续约, 再自动到期**.
//
// 顺序不能反 —— 只有先把"约定自动续约"的合同顺延掉, 剩下的才是真正该到期的合同。
//
// 三层防重复设计(锁只是第一层, 锁会丢):
//  1. Redis SET NX 锁 —— 挡住绝大多数并发
//  2. 更新条件带 status=生效中 AND version=旧值 —— 数据库层兜底, 锁失效也不会重复转移/重复顺延
//  3. 审计流水仅在 RowsAffected>0 时写 —— 一份合同只留一条对应流水
func RunDailyOnce(ctx context.Context, db *gormx.DB, rdb *redisx.Client) (DailyResult, error) {
	var res DailyResult
	if db == nil {
		return res, fmt.Errorf("数据库未初始化")
	}

	token, err := newLockToken()
	if err != nil {
		return res, fmt.Errorf("生成锁标识失败: %w", err)
	}
	lockKey := dailyLockPrefix + time.Now().Format("20060102")

	// 未抢到锁说明今天已有实例执行过(或正在执行), 属正常情况, 直接返回
	ok, err := rdb.SetNX(ctx, lockKey, token, dailyLockTTL).Result()
	if err != nil {
		return res, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !ok {
		return res, nil
	}
	defer func() {
		_, _ = releaseLockScript.Run(ctx, rdb, []string{lockKey}, token).Result()
	}()

	if res.Renewed, err = autoRenew(ctx, db); err != nil {
		return res, err
	}
	if res.Expired, err = expireDue(ctx, db); err != nil {
		return res, err
	}
	return res, nil
}

// autoRenew 把「约定自动续约」且已过终止日的生效中合同顺延租期.
//
// 只有 auto_renew=1 的合同会被顺延 —— 自动延长租期本质上是在替承租方做决定,
// 必须由合同条款显式约定; 未约定的合同走 expireDue 转为「已到期」。
func autoRenew(ctx context.Context, db *gormx.DB) (int64, error) {
	var contracts []model.LeaseContract
	if err := db.WithContext(ctx).
		Where("status = ? AND auto_renew = ? AND end_date < ?",
			model.StatusActive, model.AutoRenewOn, todayBegin()).
		Find(&contracts).Error; err != nil {
		return 0, fmt.Errorf("查询待自动续约合同失败: %w", err)
	}

	logger := logx.WithContext(ctx)
	var renewed int64

	for i := range contracts {
		c := &contracts[i]
		newEnd := renewEndDate(c.StartDate, c.EndDate)

		// 带 status + version 条件: 期间被人工续签/终止过的不会被覆盖
		res := db.WithContext(ctx).Model(&model.LeaseContract{}).
			Where("id = ? AND version = ? AND status = ?", c.Id, c.Version, model.StatusActive).
			Updates(map[string]interface{}{
				"end_date": newEnd,
				"version":  gorm.Expr("version+1"),
			})
		if res.Error != nil {
			return renewed, fmt.Errorf("合同自动续约失败: contractId=%d, %w", c.Id, res.Error)
		}
		if res.RowsAffected == 0 {
			continue
		}

		if err := db.WithContext(ctx).Create(&model.LeaseContractStatusLog{
			ContractId: c.Id,
			FromStatus: model.StatusActive,
			ToStatus:   model.StatusActive, // 续约不改变状态, 只顺延租期
			Action:     state.ActionRenew,
			Reason: fmt.Sprintf("合同约定自动续约, 租期由 %s 顺延至 %s",
				c.EndDate.Format(dateLayout), newEnd.Format(dateLayout)),
			OperatorId: 0, // 0 = 系统操作
		}).Error; err != nil {
			logger.Errorf("[cron] 写自动续约审计失败: contractId=%d, err=%v", c.Id, err)
		}

		logger.Infof("[cron] 合同自动续约: contractNo=%s, %s -> %s",
			c.ContractNo, c.EndDate.Format(dateLayout), newEnd.Format(dateLayout))
		renewed++
	}

	return renewed, nil
}

// expireDue 把终止日已过、且未约定自动续约的「生效中」合同转为「已到期」.
func expireDue(ctx context.Context, db *gormx.DB) (int64, error) {
	var contracts []model.LeaseContract
	if err := db.WithContext(ctx).
		Where("status = ? AND auto_renew = ? AND end_date < ?",
			model.StatusActive, model.AutoRenewOff, todayBegin()).
		Find(&contracts).Error; err != nil {
		return 0, fmt.Errorf("查询待到期合同失败: %w", err)
	}

	logger := logx.WithContext(ctx)
	var expired int64

	for i := range contracts {
		c := &contracts[i]
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
			Reason:     "租期届满且未约定自动续约, 系统自动到期",
			OperatorId: 0, // 0 = 系统操作
		}).Error; err != nil {
			logger.Errorf("[cron] 写合同审计失败: contractId=%d, err=%v", c.Id, err)
		}
		expired++
	}

	return expired, nil
}

// renewEndDate 计算自动续约后的新终止日: 按**原租期长度**顺延, 而不是写死一年.
//
// 理由: 原合同签了 3 年就应按 3 年续, 写死 1 年会把长期租约悄悄改成短期。
// 兜底: 原租期非法(不足 1 年)或超过 maxRenewYears 时退化为顺延 1 年, 避免脏数据造出超长租期。
func renewEndDate(start, end time.Time) time.Time {
	years := int(end.Sub(start).Hours() / 24 / 365)
	if years < 1 || years > maxRenewYears {
		return end.AddDate(1, 0, 0)
	}
	return end.AddDate(years, 0, 0)
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
