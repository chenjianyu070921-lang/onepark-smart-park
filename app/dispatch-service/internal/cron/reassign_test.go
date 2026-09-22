package cron

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"

	"onepark/app/dispatch-service/internal/assign"
	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/model"
	"onepark/common/gormx"
	"onepark/common/redisx"
)

// ---------- 纯决策分支: 任何环境都应通过 ----------

// TestDecideReassign 覆盖超时工单的三层处置分支。
//
// 这两条"释放"分支在真实环境里极难构造(需要人员池恰好为空或重派已到上限),
// 但它们恰恰决定工单会不会卡死 —— 所以必须用纯函数把每条路都锁住。
func TestDecideReassign(t *testing.T) {
	// 目标区域 Z-9F-901; A 是原处理人, B/C 是候选
	pool := []assign.Candidate{
		{AssigneeId: 1001, AssigneeName: "A-原处理人", ZoneCode: "Z-9F-901", Load: 0},
		{AssigneeId: 1002, AssigneeName: "B-同区有技能", ZoneCode: "Z-9F-901", Load: 5, Skills: []string{"fire"}},
		{AssigneeId: 1003, AssigneeName: "C-跨楼栋无技能", ZoneCode: "B-1F-101", Load: 0},
	}

	tests := []struct {
		name        string
		task        model.DispatchTask
		want        Decision
		wantID      int64
		reasonEmpty bool
	}{
		{
			name:   "有其他人可派 -> 改派, 且不派给原处理人",
			task:   model.DispatchTask{Id: 1, ZoneCode: "Z-9F-901", AssigneeId: 1001, ReassignCount: 0},
			want:   DecisionReassign,
			wantID: 1002, // 同区域, 优先于跨楼栋的 C
		},
		{
			name:   "技能需求下改派给有技能者(技能压过距离)",
			task:   model.DispatchTask{Id: 2, ZoneCode: "Z-9F-901", RequiredSkill: "fire", AssigneeId: 1001},
			want:   DecisionReassign,
			wantID: 1002,
		},
		{
			name: "重派次数达上限 -> 释放回池",
			task: model.DispatchTask{Id: 3, ZoneCode: "Z-9F-901", AssigneeId: 1001, ReassignCount: 3},
			want: DecisionRelease,
		},
		{
			name: "超过上限也照样释放",
			task: model.DispatchTask{Id: 4, ZoneCode: "Z-9F-901", AssigneeId: 1001, ReassignCount: 99},
			want: DecisionRelease,
		},
		{
			name: "排除原处理人后无人可派 -> 释放回池",
			task: model.DispatchTask{Id: 5, ZoneCode: "Z-9F-901", AssigneeId: 1001},
			// 候选池里只有原处理人一个人
			want: DecisionRelease,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := pool
			if tt.task.Id == 5 {
				p = []assign.Candidate{{AssigneeId: 1001, AssigneeName: "A-原处理人", ZoneCode: "Z-9F-901"}}
			}

			dec, cand, reason := decideReassign(&tt.task, p, 3)

			if dec != tt.want {
				t.Fatalf("Decision = %v, 期望 %v (reason=%s)", dec, tt.want, reason)
			}
			if dec == DecisionReassign {
				if cand.AssigneeId != tt.wantID {
					t.Errorf("改派对象 = %d, 期望 %d", cand.AssigneeId, tt.wantID)
				}
				// 关键不变量: 绝不能又派回原处理人, 否则每轮重派同一个人 = 死循环
				if cand.AssigneeId == tt.task.AssigneeId {
					t.Errorf("改派回了原处理人 %d —— 会形成死循环", cand.AssigneeId)
				}
			} else if reason == "" {
				t.Error("释放分支必须给出原因, 便于人工追溯")
			}
		})
	}
}

// TestDecideReassign_MaxReassignFallback maxReassign<=0 时应退回模型层默认上限.
func TestDecideReassign_MaxReassignFallback(t *testing.T) {
	task := model.DispatchTask{Id: 1, ZoneCode: "Z-9F-901", AssigneeId: 1, ReassignCount: model.MaxReassignDefault}
	pool := []assign.Candidate{{AssigneeId: 2, ZoneCode: "Z-9F-901"}}

	if dec, _, _ := decideReassign(&task, pool, 0); dec != DecisionRelease {
		t.Errorf("maxReassign=0 应退回默认上限 %d, 达到后应释放; 实际 %v",
			model.MaxReassignDefault, dec)
	}
}

// ---------- 依赖本地 MySQL + Redis 的端到端 ----------

// openTestDeps 复用服务自身的 etc/dispatch-api.yaml 连接本地依赖; 拿不到则跳过.
func openTestDeps(t *testing.T) (*gormx.DB, *redisx.Client) {
	t.Helper()

	for _, p := range []string{"../../etc/dispatch-api.yaml", "../../../etc/dispatch-api.yaml"} {
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

	t.Skip("跳过: 未找到可用的 etc/dispatch-api.yaml, 或本地 MySQL/Redis 不可用")
	return nil, nil
}

// TestRunReassignOnce_ReassignAndSkip 验证真实重派链路与两个"不该动"的场景.
//
// 测试数据的确定性做法: 造一个**独占技能标签**的处理人并把工单的 required_skill 设成它,
// 这样无论库里已有多少人员, 技能权重(10000)都保证它必然胜出。
func TestRunReassignOnce_ReassignAndSkip(t *testing.T) {
	db, rdb := openTestDeps(t)
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	uniqSkill := fmt.Sprintf("m5e2e-%d", suffix)
	winnerId := int64(880000 + suffix%100000)

	// 造一个必然胜出的处理人
	winner := &model.DispatchStaff{
		StaffId: winnerId, Name: "M5E2E Winner", ZoneCode: "Z-9F-901",
		Skills: uniqSkill, OnDuty: model.StaffOnDuty, Status: model.StaffEnabled,
	}
	if err := db.WithContext(ctx).Create(winner).Error; err != nil {
		t.Fatalf("准备人员失败: %v", err)
	}

	past := time.Now().Add(-10 * time.Minute)

	mk := func(no string, status int8, assigneeId int64, reassignCount int64) *model.DispatchTask {
		return &model.DispatchTask{
			TaskNo: no, Title: "M5E2E 超时重派", Source: model.SourceManual,
			ZoneCode: "Z-9F-901", RequiredSkill: uniqSkill,
			Priority: model.PriorityNormal, Status: status,
			AssigneeId: assigneeId, AssigneeName: "M5E2E 原处理人",
			AssignExpireAt: &past, ReassignCount: reassignCount,
		}
	}

	overdue := mk(fmt.Sprintf("DT-M5E2E-A-%d", suffix), model.StatusAssigned, winnerId-1, 0)
	processing := mk(fmt.Sprintf("DT-M5E2E-B-%d", suffix), model.StatusProcessing, winnerId-1, 0)

	for _, task := range []*model.DispatchTask{overdue, processing} {
		if err := db.WithContext(ctx).Create(task).Error; err != nil {
			t.Fatalf("准备工单失败: %v", err)
		}
	}

	t.Cleanup(func() {
		ids := []int64{overdue.Id, processing.Id}
		db.WithContext(ctx).Where("task_id IN ?", ids).Delete(&model.DispatchTaskLog{})
		db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.DispatchTask{})
		db.WithContext(ctx).Where("staff_id = ?", winnerId).Delete(&model.DispatchStaff{})
		// 释放锁, 否则同一台机器紧接着重跑会被锁挡住(表现为 0/0)
		_ = rdb.Del(ctx, reassignLockKey).Err()
	})

	res, err := RunReassignOnce(ctx, db, rdb, 3)
	if err != nil {
		t.Fatalf("RunReassignOnce 失败: %v", err)
	}
	// 只要求"至少改派了一张" —— 开发库里可能还躺着历史联调留下的超时工单,
	// 它们同样会被重派。断言全局计数等于 1 会随环境数据波动, 属于脆测试。
	// 精确性由下面针对本用例工单的逐项断言保证。
	if res.Reassigned < 1 {
		t.Errorf("Reassigned = %d, 期望 >=1(本用例的超时工单应被改派)", res.Reassigned)
	}

	// 1) 超时工单: 改派给独占技能的处理人, 且重置超时窗口、重派次数 +1
	var got model.DispatchTask
	if err := db.WithContext(ctx).First(&got, overdue.Id).Error; err != nil {
		t.Fatalf("读取工单失败: %v", err)
	}
	if got.AssigneeId != winnerId {
		t.Errorf("改派对象 = %d, 期望 %d(独占该技能的人)", got.AssigneeId, winnerId)
	}
	if got.Status != model.StatusAssigned {
		t.Errorf("改派后状态 = %d, 期望 %d(仍是已指派)", got.Status, model.StatusAssigned)
	}
	if got.ReassignCount != 1 {
		t.Errorf("ReassignCount = %d, 期望 1", got.ReassignCount)
	}
	if got.AssignExpireAt == nil || !got.AssignExpireAt.After(time.Now()) {
		t.Errorf("改派后应重置超时窗口到未来, 实际 %v", got.AssignExpireAt)
	}

	// 2) 处理中的工单: 人已接单, 不该被抢走
	var inProgress model.DispatchTask
	if err := db.WithContext(ctx).First(&inProgress, processing.Id).Error; err != nil {
		t.Fatalf("读取工单失败: %v", err)
	}
	if inProgress.AssigneeId != winnerId-1 || inProgress.Status != model.StatusProcessing {
		t.Errorf("「处理中」的工单不应被重派: assignee=%d status=%d",
			inProgress.AssigneeId, inProgress.Status)
	}

	// 3) 审计流水: 一条改派记录
	var logs []model.DispatchTaskLog
	if err := db.WithContext(ctx).Where("task_id = ?", overdue.Id).Find(&logs).Error; err != nil {
		t.Fatalf("读取审计失败: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("审计流水条数 = %d, 期望 1", len(logs))
	}
}
