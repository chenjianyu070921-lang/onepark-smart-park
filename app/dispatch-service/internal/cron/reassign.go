package cron

import (
	"context"
	"fmt"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/assign"
	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ReassignResult 一次超时重派的结果统计.
type ReassignResult struct {
	Reassigned int64 // 改派给他人的张数
	Released   int64 // 释放回「待指派」的张数(无人可派 或 达重派上限)
}

// Decision 对一张超时工单的处置决定.
type Decision int

const (
	// DecisionReassign 改派给他人
	DecisionReassign Decision = iota
	// DecisionRelease 释放回「待指派」交人工
	DecisionRelease
)

// decideReassign 纯决策函数: 给定超时工单与候选池, 决定「改派给谁」还是「释放回池」.
//
// 抽成纯函数是为了让所有分支都能被确定性覆盖 —— 「无人可派」「达上限」这两条路
// 在真实环境里很难构造(需要人员池恰好为空), 但它们恰恰是工单会不会卡死的关键。
//
// 三层兜底, 保证工单最终一定有人管:
//  1. 未达重派上限且有人可派 -> 改派给「排除原处理人」后的最优人选
//  2. 没有人可派             -> 释放回「待指派」
//  3. 重派次数已达上限       -> 释放回「待指派」
func decideReassign(t *model.DispatchTask, pool []assign.Candidate, maxReassign int64) (Decision, assign.Candidate, string) {
	if maxReassign <= 0 {
		maxReassign = model.MaxReassignDefault
	}

	// 达上限: 不再自动改派。
	// 继续重派只会每轮刷同样的日志, 并掩盖「真的没人可派」这个事实。
	if t.ReassignCount >= maxReassign {
		return DecisionRelease, assign.Candidate{},
			fmt.Sprintf("重派 %d 次仍无人接单, 释放回待指派交人工", t.ReassignCount)
	}

	// 排除原处理人 —— 不排除就会原样再派给他, 变成死循环
	best, found := assign.Pick(t.ZoneCode, t.RequiredSkill, assign.Without(pool, t.AssigneeId))
	if !found {
		return DecisionRelease, assign.Candidate{},
			"指派超时且无其他可用处理人, 释放回待指派交人工"
	}

	return DecisionReassign, best, ""
}

// RunReassignOnce 扫描「已指派但超时未接单」的工单并处理.
//
// 只处理 status=已指派(2): 「处理中」(3)说明人已经接单, 重派等于把人正在干的活抢走。
//
// 并发防护三层(锁会丢, 不能只靠锁):
//  1. Redis SET NX 锁 + Lua 比对释放 —— 挡住多实例
//  2. 更新条件带 status + version —— 数据库层兜底, 锁失效也不会重复改派
//  3. 审计流水仅在 RowsAffected>0 时写 —— 一张单一次改派只留一条流水
func RunReassignOnce(ctx context.Context, db *gormx.DB, rdb *redisx.Client, maxReassign int64) (ReassignResult, error) {
	var res ReassignResult
	if db == nil {
		return res, fmt.Errorf("数据库未初始化")
	}
	if rdb == nil {
		return res, fmt.Errorf("redis 未初始化")
	}
	if maxReassign <= 0 {
		maxReassign = model.MaxReassignDefault
	}

	token, err := newLockToken()
	if err != nil {
		return res, fmt.Errorf("生成锁标识失败: %w", err)
	}
	ok, err := rdb.SetNX(ctx, reassignLockKey, token, reassignLockTTL).Result()
	if err != nil {
		return res, fmt.Errorf("获取分布式锁失败: %w", err)
	}
	if !ok {
		// 说明另一个实例正在跑, 属正常情况
		return res, nil
	}
	defer func() {
		_, _ = releaseLockScript.Run(ctx, rdb, []string{reassignLockKey}, token).Result()
	}()

	logger := logx.WithContext(ctx)

	// 已超时的「已指派」工单, 先到期的先处理(最急的那张优先拿到人手)
	var tasks []model.DispatchTask
	if err := db.WithContext(ctx).
		Where("status = ? AND assign_expire_at IS NOT NULL AND assign_expire_at < ?",
			model.StatusAssigned, time.Now()).
		Order("assign_expire_at ASC").
		Find(&tasks).Error; err != nil {
		return res, fmt.Errorf("查询超时工单失败: %w", err)
	}
	if len(tasks) == 0 {
		return res, nil
	}

	// 候选池只载一次: 池本身在循环内不会变。
	// 但必须把"本轮已派出去的负载"累加回内存 —— 否则一批积压工单会拿着同一份
	// 陈旧负载反复选中同一个人, 负载均衡当场失效。
	pool, source, err := assign.Pool(ctx, db)
	if err != nil {
		return res, fmt.Errorf("载入候选池失败: %w", err)
	}
	logger.Infof("[cron] 超时重派开始: 待处理 %d 张, 候选池=%s(%d 人)", len(tasks), source, len(pool))

	for i := range tasks {
		t := &tasks[i]

		dec, best, reason := decideReassign(t, pool, maxReassign)

		if dec == DecisionRelease {
			released, rerr := releaseTask(ctx, db, t, reason)
			if rerr != nil {
				logger.Errorf("[cron] 释放工单失败: taskId=%d, err=%v", t.Id, rerr)
				continue
			}
			if released {
				res.Released++
			}
			continue
		}

		reassigned, rerr := reassignTask(ctx, db, t, best)
		if rerr != nil {
			logger.Errorf("[cron] 改派失败: taskId=%d, err=%v", t.Id, rerr)
			continue
		}
		if !reassigned {
			continue // 期间被人工处理过, 跳过
		}

		bd := assign.Explain(t.ZoneCode, t.RequiredSkill, best)
		logger.Infof("[cron] 超时改派: taskId=%d, 原处理人=%d(%s) -> 新处理人=%d(%s), "+
			"技能命中=%v, 距离=%d, 负载=%d, 得分=%d, 第 %d 次重派",
			t.Id, t.AssigneeId, t.AssigneeName, best.AssigneeId, best.AssigneeName,
			bd.SkillMatched, bd.Distance, bd.Load, bd.Score, t.ReassignCount+1)

		// 把本次派出去的负载记回内存, 后续工单才能看到最新负载
		for j := range pool {
			if pool[j].AssigneeId == best.AssigneeId {
				pool[j].Load++
				break
			}
		}
		res.Reassigned++
	}

	return res, nil
}

// reassignTask 把工单改派给新处理人, 并重置超时窗口、累加重派次数.
// 返回 false 表示工单在读取后被他人改动过(status/version 不匹配), 本轮跳过。
func reassignTask(ctx context.Context, db *gormx.DB, t *model.DispatchTask, to assign.Candidate) (bool, error) {
	res := db.WithContext(ctx).Model(&model.DispatchTask{}).
		Where("id = ? AND version = ? AND status = ?", t.Id, t.Version, model.StatusAssigned).
		Updates(map[string]interface{}{
			"assignee_id":   to.AssigneeId,
			"assignee_name": to.AssigneeName,
			// 重置超时窗口: 新处理人同样需要一个接单时限
			"assign_expire_at": time.Now().Add(model.AssignExpireWindow),
			"reassign_count":   gorm.Expr("reassign_count+1"),
			"version":          gorm.Expr("version+1"),
		})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}

	writeLog(ctx, db, t.Id, model.StatusAssigned, model.StatusAssigned, state.ActionAssign,
		fmt.Sprintf("指派超时自动改派: %s -> %s", t.AssigneeName, to.AssigneeName))
	return true, nil
}

// releaseTask 把工单释放回「待指派」: 清空处理人与超时点, 交人工处置。
func releaseTask(ctx context.Context, db *gormx.DB, t *model.DispatchTask, reason string) (bool, error) {
	to, ok := state.Next(model.StatusAssigned, state.ActionRelease)
	if !ok {
		// 状态机不允许该转移属代码问题, 必须暴露
		return false, fmt.Errorf("状态机不允许从 %d 执行 %s", model.StatusAssigned, state.ActionRelease)
	}

	res := db.WithContext(ctx).Model(&model.DispatchTask{}).
		Where("id = ? AND version = ? AND status = ?", t.Id, t.Version, model.StatusAssigned).
		Updates(map[string]interface{}{
			"status":           to,
			"assignee_id":      0,
			"assignee_name":    "",
			"assign_expire_at": nil,
			"version":          gorm.Expr("version+1"),
		})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}

	logx.WithContext(ctx).Infof("[cron] 工单释放回待指派: taskId=%d, 原处理人=%d(%s), 原因=%s",
		t.Id, t.AssigneeId, t.AssigneeName, reason)

	writeLog(ctx, db, t.Id, model.StatusAssigned, to, state.ActionRelease, reason)
	return true, nil
}

// writeLog 写状态流转审计.
//
// 刻意不与状态更新放同一事务: 状态变更是主流程, 审计失败只记日志不回滚 ——
// 「改了但没留痕」比「留了痕但没改」危害小得多, 也不会让定时任务整体失败。
func writeLog(ctx context.Context, db *gormx.DB, taskId int64, from, to int8, action, remark string) {
	if err := db.WithContext(ctx).Create(&model.DispatchTaskLog{
		TaskId:     taskId,
		FromStatus: from,
		ToStatus:   to,
		Action:     action,
		Remark:     remark,
		OperatorId: 0, // 0 = 系统操作
	}).Error; err != nil {
		logx.WithContext(ctx).Errorf("[cron] 写工单审计失败: taskId=%d, err=%v", taskId, err)
	}
}
