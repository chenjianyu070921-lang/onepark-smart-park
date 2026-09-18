package lease

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/ecode"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/errorx"
)

// billLockTTL 分布式锁超时时间, 需显著大于预估最长执行时间.
const billLockTTL = 10 * time.Minute

// releaseLockScript 释放锁时必须比对持有者 token, 不能直接 DEL.
// 否则: A 超时后锁自动过期, B 拿到锁, A 执行完把 B 的锁删了 → C 又能拿到锁 → 并发执行。
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`)

// BillAutoLogic 自动生成月度租金账单.
type BillAutoLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewBillAutoLogic 构造账单生成逻辑.
func NewBillAutoLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillAutoLogic {
	return &BillAutoLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// BillAuto 为所有「生效中」合同生成指定账期的租金账单.
//
// 幂等策略(两层):
//  1. 唯一索引 uk_contract_period(contract_id, billing_period) —— 幂等的根基, 即使锁失效也不会重复
//  2. Redis 分布式锁 —— 只用于避免多实例同时跑做无用功, 不承担幂等职责(锁会丢)
func (l *BillAutoLogic) BillAuto(req *types.BillAutoReq) (*types.BillAutoResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	period := req.Period
	if period == "" {
		period = time.Now().AddDate(0, -1, 0).Format("2006-01")
	}
	if _, err := time.Parse("2006-01", period); err != nil {
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "账期格式应为 yyyy-MM")
	}

	lockKey := fmt.Sprintf("m5:lease:bill:lock:%s", period)
	token := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int63())

	if l.svcCtx.Redis != nil {
		ok, err := l.svcCtx.Redis.SetNX(l.ctx, lockKey, token, billLockTTL).Result()
		if err != nil {
			l.Errorf("[lease] acquire bill lock failed: %v", err)
			return nil, errorx.NewError(ecode.ErrBillGenerateFailed, "获取账单生成锁失败")
		}
		if !ok {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "该账期账单正在生成中, 请稍后重试")
		}
		// 释放锁用独立 context: 请求 context 可能已取消, 但锁必须释放
		defer l.releaseLock(lockKey, token)
	}

	var contracts []model.LeaseContract
	if err := l.svcCtx.DB.WithContext(l.ctx).
		Where("status = ?", model.StatusActive).
		Find(&contracts).Error; err != nil {
		l.Errorf("[lease] load active contracts failed: %v", err)
		return nil, errorx.NewError(ecode.ErrBillGenerateFailed, "加载生效合同失败")
	}

	resp := &types.BillAutoResp{Period: period}
	for i := range contracts {
		c := contracts[i]
		bill := &model.LeaseBill{
			BillNo:        fmt.Sprintf("BL%s%06d", strings.ReplaceAll(period, "-", ""), c.Id),
			ContractId:    c.Id,
			TenantId:      c.TenantId,
			BillingPeriod: period,
			Amount:        c.MonthlyRent, // decimal 原值, 不做浮点运算
			Status:        model.BillStatusUnpaid,
		}

		if err := l.svcCtx.DB.WithContext(l.ctx).Create(bill).Error; err != nil {
			if isDuplicateEntry(err) {
				// 该账期账单已存在 -> 幂等跳过
				resp.Skipped++
				continue
			}
			// 真实失败(连接断开/字段超长等): 单条失败不中断整批, 但必须计数并在末尾上报,
			// 禁止混入 Skipped 静默吞掉 —— 否则出账部分丢失无感知.
			l.Errorf("[lease] create bill failed: contractId=%d, err=%v", c.Id, err)
			resp.Failed++
			continue
		}
		resp.Created++
	}

	l.Infof("[lease] bill auto done: period=%s, created=%d, skipped=%d, failed=%d",
		resp.Period, resp.Created, resp.Skipped, resp.Failed)
	if resp.Failed > 0 {
		return resp, errorx.NewError(ecode.ErrBillGenerateFailed,
			fmt.Sprintf("出账完成但 %d 条失败, 请结合日志人工复核", resp.Failed))
	}
	return resp, nil
}

// releaseLock 用 Lua 比对 token 后删除锁.
func (l *BillAutoLogic) releaseLock(key, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := releaseLockScript.Run(ctx, l.svcCtx.Redis, []string{key}, token).Err(); err != nil {
		l.Errorf("[lease] release bill lock failed: %v", err)
	}
}

// isDuplicateEntry 判断是否唯一键冲突(MySQL 1062).
// 用错误信息匹配而非引入 mysql 驱动包, 避免为一次判断新增直接依赖.
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
