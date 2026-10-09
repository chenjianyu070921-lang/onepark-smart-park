// Package job 定时任务: 每天自动巡检前一天的能耗, 产出优化建议
package job

import (
	"context"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/energy-agent-service/internal/logic"
	"onepark/app/energy-agent-service/internal/svc"
)

// InspectJob 定时巡检
//
// 和计费的定时任务一样, 不引 cron 库: 一天才跑一次, 每小时看一眼
// "到点没 + 今天跑过没"就够了。真要精确到秒再换 robfig/cron。
//
// 关键: 这里调用的是和手动触发接口同一个 RunInspect,
// 所以"自动扫出来的"和"手动点出来的"报告完全一致。
type InspectJob struct {
	svcCtx *svc.ServiceContext
	// hour 每天几点跑, 默认早上 7 点(扫前一天的数据)
	hour int
	// lastDate 已经跑过的日期, 避免同一天反复扫
	lastDate string
	mu       sync.Mutex
	cancel   context.CancelFunc
}

func NewInspectJob(svcCtx *svc.ServiceContext, hour int) *InspectJob {
	if hour < 0 || hour > 23 {
		hour = 7
	}
	return &InspectJob{svcCtx: svcCtx, hour: hour}
}

// Start 实现 go-zero 的 service.Service, 由 ServiceGroup 拉起
func (j *InspectJob) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	go j.loop(ctx)
	logx.Infof("定时巡检任务已启动, 每天 %d 点扫前一天的数据", j.hour)
}

// Stop 实现 go-zero 的 service.Service
func (j *InspectJob) Stop() {
	if j.cancel != nil {
		j.cancel()
	}
	logx.Info("定时巡检任务已停止")
}

func (j *InspectJob) loop(ctx context.Context) {
	// 启动先跑一次: 万一到点那天服务没开, 这次给补上
	j.tryRun(time.Now())

	ticker := time.NewTicker(time.Hour)
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

// tryRun 到点了就扫一次, 同一天只扫一次
func (j *InspectJob) tryRun(now time.Time) {
	if now.Hour() < j.hour {
		return // 还没到点
	}
	// 扫的是昨天: 今天的数据还没上报齐, 拿半天比一整天必然误报
	statDate := now.AddDate(0, 0, -1)
	key := statDate.Format("2006-01-02")

	j.mu.Lock()
	if j.lastDate == key {
		j.mu.Unlock()
		return
	}
	j.lastDate = key
	j.mu.Unlock()

	j.runInspect(statDate)
}

func (j *InspectJob) runInspect(statDate time.Time) {
	ctx := context.Background()
	res, err := logic.RunInspect(ctx, j.svcCtx, logic.InspectInput{
		StatDate: statDate,
		Trigger:  "cron",
	})
	if err != nil {
		logx.Errorf("定时巡检失败 日期=%s err=%v", statDate.Format("2006-01-02"), err)
		return
	}
	logx.Infof("定时巡检完成 日期=%s 区域=%d 发现=%d 大模型=%v 耗时=%dms",
		res.StatDate, res.ZoneCount, len(res.Findings), res.LLMEnabled, res.CostMs)

	// 高严重度的单独打一条, 方便在日志里直接 grep 到该处理的问题
	for _, f := range res.Findings {
		if f.Severity >= 3 {
			logx.Infof("巡检发现[高] %s %s %s", f.ZoneID, f.DeviceID, f.Title)
		}
	}
}
