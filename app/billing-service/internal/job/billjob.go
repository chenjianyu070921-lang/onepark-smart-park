// Package job 定时任务: 接口61 的定时触发, 每月自动给上个自然月出账单
package job

import (
	"context"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/billing-service/internal/logic"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// BillJob 定时出账
//
// 为什么不用现成的 cron 库: 一个月才跑一次, 引入依赖不划算;
// 每小时看一眼"到日子没 + 这个月跑过没"就够了。
// 真要更精确(比如凌晨两点整)再换 robfig/cron。
type BillJob struct {
	svcCtx *svc.ServiceContext
	// dayOfMonth 每月几号出上个月的账, 默认 1 号
	dayOfMonth int
	// lastPeriod 已经跑过的账期, 避免同一个月反复出账
	lastPeriod string
	mu         sync.Mutex
	cancel     context.CancelFunc
}

func NewBillJob(svcCtx *svc.ServiceContext, dayOfMonth int) *BillJob {
	// 只接受 1~28, 免得定到 31 号结果小月永远不触发
	if dayOfMonth < 1 || dayOfMonth > 28 {
		dayOfMonth = 1
	}
	return &BillJob{svcCtx: svcCtx, dayOfMonth: dayOfMonth}
}

// Start 实现 go-zero 的 service.Service, 由 ServiceGroup 一起拉起
func (j *BillJob) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	go j.loop(ctx)
	logx.Infof("定时出账任务已启动, 每月 %d 号给上个月的用量出账", j.dayOfMonth)
}

// Stop 实现 go-zero 的 service.Service
func (j *BillJob) Stop() {
	if j.cancel != nil {
		j.cancel()
	}
	logx.Info("定时出账任务已停止")
}

func (j *BillJob) loop(ctx context.Context) {
	// 启动先跑一次: 万一到日子那天服务没开, 这次给补上
	j.tryRun(time.Now())

	ticker := time.NewTicker(time.Hour) // 一个月才一次, 每小时看一眼足够
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			j.tryRun(time.Now())
		}
	}
}

// tryRun 到日子了就出一次账, 同一个账期只出一次
func (j *BillJob) tryRun(now time.Time) {
	if now.Day() < j.dayOfMonth {
		return // 还没到日子
	}
	// 出的是上一个自然月的账
	period := now.AddDate(0, -1, 0).Format("2006-01")

	j.mu.Lock()
	if j.lastPeriod == period {
		j.mu.Unlock()
		return
	}
	j.lastPeriod = period
	j.mu.Unlock()

	j.generateAll(period)
}

// generateAll 给所有有数据的区域逐个出账, 一个区域出问题不影响别的区域
func (j *BillJob) generateAll(period string) {
	start, end, ok := logic.ParsePeriod(period)
	if !ok {
		logx.Errorf("定时出账: 账期格式不对 %s", period)
		return
	}

	ctx := context.Background()
	zones, err := j.svcCtx.EnergyReading.ListZones(ctx, start, end)
	if err != nil {
		logx.Errorf("定时出账: 查区域列表失败 err=%v", err)
		return
	}
	if len(zones) == 0 {
		logx.Infof("定时出账: %s 没有区域上报过数据, 跳过", period)
		return
	}

	done, skip := 0, 0
	for _, z := range zones {
		// 复用接口61 的逻辑, 保证手动出账和定时出账算出来的钱一模一样
		l := logic.NewBillGenerateLogic(ctx, j.svcCtx)
		if _, err := l.BillGenerate(&types.BillGenerateRequest{ZoneId: z, Period: period}); err != nil {
			// "这个区域没数据""没配规则""已经出过账了"都属于预期内, 记 info 就行
			logx.Infof("定时出账: 区域 %s 跳过, 原因=%v", z, err)
			skip++
			continue
		}
		done++
		logx.Infof("定时出账: 区域 %s 账期 %s 出账完成", z, period)
	}
	logx.Infof("定时出账结束 账期=%s 出账=%d 跳过=%d", period, done, skip)
}
