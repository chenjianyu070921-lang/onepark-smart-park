package cron

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/zeromicro/go-zero/core/conf"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/model"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ---------- 纯函数部分: 任何环境都应通过 ----------

// TestRenewEndDate 验证自动续约按「原租期长度」顺延, 以及脏数据的兜底.
func TestRenewEndDate(t *testing.T) {
	d := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02", s, time.Local)
		if err != nil {
			t.Fatalf("测试数据日期非法: %s", s)
		}
		return v
	}

	tests := []struct {
		name       string
		start, end string
		want       string
	}{
		{"一年期续一年", "2026-01-01", "2027-01-01", "2028-01-01"},
		{"三年期续三年(不是写死一年)", "2025-01-01", "2028-01-01", "2031-01-01"},
		{"跨闰年仍按年顺延", "2024-01-01", "2025-01-01", "2026-01-01"},
		{"不足一年 -> 兜底顺延一年", "2026-01-01", "2026-07-01", "2027-07-01"},
		{"租期非法(end<=start) -> 兜底一年", "2026-01-01", "2026-01-01", "2027-01-01"},
		{"超长租期(>30年) -> 兜底一年", "2000-01-01", "2060-01-01", "2061-01-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renewEndDate(d(tt.start), d(tt.end))
			if want := d(tt.want); !got.Equal(want) {
				t.Fatalf("renewEndDate = %s, 期望 %s", got.Format(dateLayout), tt.want)
			}
			// 续约后必须严格晚于原终止日, 否则等于没续
			if !got.After(d(tt.end)) {
				t.Errorf("续约后终止日(%s)必须晚于原终止日(%s)", got.Format(dateLayout), tt.end)
			}
		})
	}
}

// ---------- 依赖本地 MySQL + Redis 的部分: 拿不到配置则跳过 ----------

// openTestDeps 复用服务自身的 etc/leasing-api.yaml 连接本地依赖.
// 好处: 不引入额外测试配置, 且与运行期连的是同一个库。
func openTestDeps(t *testing.T) (*gormx.DB, *redisx.Client) {
	t.Helper()

	for _, p := range []string{"../../etc/leasing-api.yaml", "../../../etc/leasing-api.yaml"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		var c config.Config
		// conf.UseEnv() 必须开: 配置里的 DSN/Redis 已是 ${VAR} 占位符(平台统一要求),
		// 不开则加载到的是字面量, 连接必然失败 -> DB 用例**静默跳过**(go test 仍打印 ok)。
		if err := conf.Load(p, &c, conf.UseEnv()); err != nil {
			continue
		}
		if c.MySQL.DataSource == "" {
			continue
		}

		db, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			continue
		}
		if err := sqlDB.Ping(); err != nil {
			continue
		}

		rdb := redisx.NewClient(&c.Redis)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := rdb.Ping(ctx).Err(); err != nil {
			continue
		}

		return db, rdb
	}

	t.Skip("跳过: 未找到可用的 etc/leasing-api.yaml, 或本地 MySQL/Redis 不可用")
	return nil, nil
}

// TestRunDailyOnce_AutoRenewAndExpire 验证每日维护的两条分支与顺序:
//   - auto_renew=1 且已过终止日 -> 顺延租期, 状态保持「生效中」
//   - auto_renew=0 且已过终止日 -> 转「已到期」
//   - 未到终止日 -> 两者都不动
func TestRunDailyOnce_AutoRenewAndExpire(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	no := func(i int) string { return fmt.Sprintf("LC-CRON-%d-%d", suffix, i) }

	mk := func(no string, start, end string, autoRenew int8) *model.LeaseContract {
		parse := func(s string) time.Time {
			v, _ := time.ParseInLocation(dateLayout, s, time.Local)
			return v
		}
		return &model.LeaseContract{
			ContractNo: no, TenantId: 9001, TenantName: "Cron Test Co",
			ZoneCode: "Z-1F-101", AreaSqm: 100,
			MonthlyRent: decimal.Zero, Deposit: decimal.Zero,
			StartDate: parse(start), EndDate: parse(end),
			Status:    model.StatusActive,
			AutoRenew: autoRenew, RenewNoticeDays: 30,
		}
	}

	today := time.Now()
	// 1) 一年期、约定自动续约、已过终止日 -> 应顺延到明年同日
	overdueRenew := mk(no(1), today.AddDate(-1, 0, 0).Format(dateLayout),
		today.AddDate(0, 0, -1).Format(dateLayout), model.AutoRenewOn)
	// 2) 未约定续约、已过终止日 -> 应转已到期
	overdueExpire := mk(no(2), today.AddDate(-1, 0, 0).Format(dateLayout),
		today.AddDate(0, 0, -1).Format(dateLayout), model.AutoRenewOff)
	// 3) 未约定续约、尚未到期 -> 两者都不应动
	future := mk(no(3), today.Format(dateLayout),
		today.AddDate(0, 0, 30).Format(dateLayout), model.AutoRenewOff)

	for _, c := range []*model.LeaseContract{overdueRenew, overdueExpire, future} {
		if err := db.WithContext(ctx).Create(c).Error; err != nil {
			t.Fatalf("准备测试数据失败: %v", err)
		}
	}

	lockKey := dailyLockPrefix + time.Now().Format("20060102")
	t.Cleanup(func() {
		ids := []int64{overdueRenew.Id, overdueExpire.Id, future.Id}
		db.WithContext(ctx).Where("contract_id IN ?", ids).Delete(&model.LeaseContractStatusLog{})
		db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.LeaseContract{})
		// 释放当日锁, 否则同一台机器当天重跑本测试会被锁挡住(表现为 0/0)
		_ = rdb.Del(ctx, lockKey).Err()
	})

	res, err := RunDailyOnce(ctx, db, rdb)
	if err != nil {
		t.Fatalf("RunDailyOnce 失败: %v", err)
	}
	if res.Renewed != 1 {
		t.Errorf("Renewed = %d, 期望 1", res.Renewed)
	}
	if res.Expired != 1 {
		t.Errorf("Expired = %d, 期望 1", res.Expired)
	}

	// 1) 续约: 终止日按原租期(1 年)顺延, 状态仍是生效中
	var got1 model.LeaseContract
	if err := db.WithContext(ctx).First(&got1, overdueRenew.Id).Error; err != nil {
		t.Fatalf("读取续约合同失败: %v", err)
	}
	wantEnd := renewEndDate(overdueRenew.StartDate, overdueRenew.EndDate)
	if !got1.EndDate.Equal(wantEnd) {
		t.Errorf("续约后终止日 = %s, 期望 %s",
			got1.EndDate.Format(dateLayout), wantEnd.Format(dateLayout))
	}
	if got1.Status != model.StatusActive {
		t.Errorf("自动续约后状态 = %d, 期望 %d(生效中)", got1.Status, model.StatusActive)
	}
	if got1.Version != overdueRenew.Version+1 {
		t.Errorf("乐观锁 version = %d, 期望 %d", got1.Version, overdueRenew.Version+1)
	}

	// 2) 到期: 状态转为已到期
	var got2 model.LeaseContract
	if err := db.WithContext(ctx).First(&got2, overdueExpire.Id).Error; err != nil {
		t.Fatalf("读取到期合同失败: %v", err)
	}
	if got2.Status != model.StatusExpired {
		t.Errorf("未约定续约的逾期合同状态 = %d, 期望 %d(已到期)", got2.Status, model.StatusExpired)
	}

	// 3) 未到期: 原样不动
	var got3 model.LeaseContract
	if err := db.WithContext(ctx).First(&got3, future.Id).Error; err != nil {
		t.Fatalf("读取未到期合同失败: %v", err)
	}
	if got3.Status != model.StatusActive || !got3.EndDate.Equal(future.EndDate) {
		t.Errorf("未到期合同不应被改动: status=%d end=%s", got3.Status, got3.EndDate.Format(dateLayout))
	}

	// 审计流水: 续约与到期各一条, 且 action 正确
	var logs []model.LeaseContractStatusLog
	if err := db.WithContext(ctx).
		Where("contract_id IN ?", []int64{overdueRenew.Id, overdueExpire.Id}).
		Find(&logs).Error; err != nil {
		t.Fatalf("读取审计流水失败: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("审计流水条数 = %d, 期望 2(续约1 + 到期1)", len(logs))
	}
}

// TestRunDailyOnce_LockGuards 验证分布式锁: 同一天第二次执行直接返回 0/0, 不会重复处理.
func TestRunDailyOnce_LockGuards(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()

	lockKey := dailyLockPrefix + time.Now().Format("20060102")
	// 先占住当天的锁, 模拟"另一个实例已经跑过"
	if err := rdb.Set(ctx, lockKey, "other-instance", dailyLockTTL).Err(); err != nil {
		t.Fatalf("占锁失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Del(ctx, lockKey).Err() })

	res, err := RunDailyOnce(ctx, db, rdb)
	if err != nil {
		t.Fatalf("RunDailyOnce 不应报错(未抢到锁是正常情况): %v", err)
	}
	if res.Renewed != 0 || res.Expired != 0 {
		t.Errorf("未抢到锁时不应处理任何合同, 实际 %+v", res)
	}
}
