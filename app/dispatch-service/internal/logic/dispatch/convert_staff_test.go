package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
)

// ---------- 纯函数 ----------

// TestClampTaskPage 分页兜底.
//
// 为什么值得测: pageSize 不钳制的话, 前端传个 100000 就会**一次扫全表**,
// 在工单表长起来之后是实打实的性能事故; page<1 则会生成 OFFSET 负数。
func TestClampTaskPage(t *testing.T) {
	cases := []struct {
		name               string
		page, pageSize     int64
		wantPage, wantSize int64
	}{
		{"正常值原样返回", 3, 20, 3, 20},
		{"page=0 提到 1", 0, 20, 1, 20},
		{"page 为负提到 1", -5, 20, 1, 20},
		{"pageSize=0 兜底为 10", 1, 0, 1, 10},
		{"pageSize 为负兜底为 10", 1, -1, 1, 10},
		{"正好等于上限不钳制", 1, maxPageSize, 1, maxPageSize},
		{"超上限被钳制", 1, 1000, 1, maxPageSize},
		{"上限+1 被钳制", 1, maxPageSize + 1, 1, maxPageSize},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPage, gotSize := clampTaskPage(c.page, c.pageSize)
			if gotPage != c.wantPage {
				t.Errorf("page: got=%d, want=%d", gotPage, c.wantPage)
			}
			if gotSize != c.wantSize {
				t.Errorf("pageSize: got=%d, want=%d", gotSize, c.wantSize)
			}
		})
	}
}

// TestToTaskDTO_NilFieldsNotZero 可空字段必须保持"空", 不能退化成 0 值.
//
// AssignExpireAt 尤其关键: 退化成 0 会被前端读成 1970 年,
// 运维看到"1970 年超时"只会一脸问号; nil 才表示"当前没有待接单的指派窗口"。
func TestToTaskDTO_NilFieldsNotZero(t *testing.T) {
	dto := toTaskDTO(&model.DispatchTask{Id: 7, TaskNo: "DT-1", Status: model.StatusPendingAssign})

	if dto.AssignExpireAt != nil {
		t.Errorf("未指派时 AssignExpireAt 应为 nil, 实际 %v", *dto.AssignExpireAt)
	}
	if dto.AlarmId != "" {
		t.Errorf("非告警来源时 AlarmId 应为空串, 实际 %q", dto.AlarmId)
	}
	if dto.Id != 7 || dto.TaskNo != "DT-1" || dto.Status != int32(model.StatusPendingAssign) {
		t.Errorf("基础字段透传错误: %+v", dto)
	}

	// 有值时必须原样透出
	exp := time.Now().Truncate(time.Second)
	alarmId := "AL-TEST-1"
	withValues := toTaskDTO(&model.DispatchTask{
		Id: 8, AssignExpireAt: &exp, AlarmId: &alarmId, ReassignCount: 2,
	})
	if withValues.AssignExpireAt == nil || *withValues.AssignExpireAt != exp.Unix() {
		t.Errorf("AssignExpireAt 未正确透出: got=%v want=%d", withValues.AssignExpireAt, exp.Unix())
	}
	if withValues.AlarmId != alarmId {
		t.Errorf("AlarmId = %q, 期望 %q", withValues.AlarmId, alarmId)
	}
	if withValues.ReassignCount != 2 {
		t.Errorf("ReassignCount = %d, 期望 2", withValues.ReassignCount)
	}
}

// TestToStaffDTO_FieldMapping 人员 DTO 字段透传.
func TestToStaffDTO_FieldMapping(t *testing.T) {
	dto := toStaffDTO(&model.DispatchStaff{
		StaffId: 1201, Name: "王工", Phone: "13800000000", ZoneCode: "Z-1F-101",
		Skills: "fire,electrical", OnDuty: model.StaffOnDuty, Status: model.StaffEnabled,
	})
	if dto.StaffId != 1201 || dto.Name != "王工" || dto.Phone != "13800000000" {
		t.Errorf("基础字段透传错误: %+v", dto)
	}
	if dto.ZoneCode != "Z-1F-101" || dto.Skills != "fire,electrical" {
		t.Errorf("区域/技能透传错误: %+v", dto)
	}
	if dto.OnDuty != int32(model.StaffOnDuty) || dto.Status != int32(model.StaffEnabled) {
		t.Errorf("在岗/启用状态透传错误: %+v", dto)
	}
}

// ---------- 人员 upsert ----------

// TestStaffUpsert_Validation 人员写入的入参校验.
func TestStaffUpsert_Validation(t *testing.T) {
	svcCtx := &svc.ServiceContext{DB: openTestDB(t)}
	ctx := context.Background()
	logic := NewStaffUpsertLogic(ctx, svcCtx)

	base := func() *types.StaffUpsertReq {
		return &types.StaffUpsertReq{
			StaffId: 1201, Name: "校验用", ZoneCode: "Z-TEST-1F",
			OnDuty: int32(model.StaffOnDuty), Status: int32(model.StaffEnabled),
		}
	}

	cases := []struct {
		name   string
		mutate func(*types.StaffUpsertReq)
	}{
		{"staff_id 为 0", func(r *types.StaffUpsertReq) { r.StaffId = 0 }},
		{"staff_id 为负", func(r *types.StaffUpsertReq) { r.StaffId = -1 }},
		{"姓名为空", func(r *types.StaffUpsertReq) { r.Name = "" }},
		{"常驻区域为空", func(r *types.StaffUpsertReq) { r.ZoneCode = "" }},
		{"on_duty 非法", func(r *types.StaffUpsertReq) { r.OnDuty = 2 }},
		{"status 非法", func(r *types.StaffUpsertReq) { r.Status = 9 }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := base()
			c.mutate(req)
			if _, err := logic.StaffUpsert(req); err == nil {
				t.Errorf("%s: 应被拒绝, 但通过了", c.name)
			}
		})
	}
}

// TestStaffUpsert_IdempotentAndNormalizes 以 staff_id 为业务键幂等: 重复提交不产生重复行,
// 且技能会被归一化("Fire" 与 "fire" 必须能匹配上, 否则自动指派的技能判定会失效).
func TestStaffUpsert_IdempotentAndNormalizes(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewStaffUpsertLogic(ctx, svcCtx)

	staffId := 900_000 + time.Now().UnixNano()%100_000
	t.Cleanup(func() { db.Where("staff_id = ?", staffId).Delete(&model.DispatchStaff{}) })

	messy := "  Fire , fire ,  ELECTRICAL "
	if _, err := logic.StaffUpsert(&types.StaffUpsertReq{
		StaffId: staffId, Name: "初版姓名", ZoneCode: "Z-TEST-1F",
		Skills: messy, OnDuty: int32(model.StaffOnDuty), Status: int32(model.StaffEnabled),
	}); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}

	// 再来一次(改姓名), 应该走更新而不是新增
	if _, err := logic.StaffUpsert(&types.StaffUpsertReq{
		StaffId: staffId, Name: "改后姓名", ZoneCode: "Z-TEST-2F",
		Skills: "security", OnDuty: int32(model.StaffOffDuty), Status: int32(model.StaffEnabled),
	}); err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}

	var rows []model.DispatchStaff
	if err := db.Where("staff_id = ?", staffId).Find(&rows).Error; err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("同一 staff_id 应只有 1 行, 实际 %d 行(upsert 退化成 insert 了)", len(rows))
	}
	got := rows[0]
	if got.Name != "改后姓名" || got.ZoneCode != "Z-TEST-2F" {
		t.Errorf("二次写入未覆盖字段: name=%q zone=%q", got.Name, got.ZoneCode)
	}
	if got.OnDuty != model.StaffOffDuty {
		t.Errorf("on_duty = %d, 期望被更新为不在岗", got.OnDuty)
	}
	if got.Skills != model.NormalizeSkills("security") {
		t.Errorf("技能未归一化: %q", got.Skills)
	}
}

// ---------- 自动指派主路径 ----------

// TestTaskAssign_AutoPicksSkillMatchedCandidate 自动指派(assignee_id=0)应真的选出人并落库.
//
// 确定性做法(与 internal/cron 的重派用例同源): 造一个**独占技能标签**的在岗人员,
// 并把工单的 required_skill 设成它 —— 技能权重(10000)保证它必然胜出,
// 不依赖库里已有多少人员。
func TestTaskAssign_AutoPicksSkillMatchedCandidate(t *testing.T) {
	db := openTestDB(t)
	svcCtx := &svc.ServiceContext{DB: db}
	ctx := context.Background()
	logic := NewTaskAssignLogic(ctx, svcCtx)

	uniq := fmt.Sprintf("m5-auto-%d", time.Now().UnixNano())
	staffId := 900_000 + time.Now().UnixNano()%100_000

	if err := db.Create(&model.DispatchStaff{
		StaffId: staffId, Name: "自动指派命中者", ZoneCode: "Z-TEST-1F",
		Skills: uniq, OnDuty: model.StaffOnDuty, Status: model.StaffEnabled,
	}).Error; err != nil {
		t.Fatalf("准备人员失败: %v", err)
	}
	t.Cleanup(func() { db.Where("staff_id = ?", staffId).Delete(&model.DispatchStaff{}) })

	task := newTestTask(t, db, model.StatusPendingAssign, 0)
	// 工单要求这个独占技能
	if err := db.Model(&model.DispatchTask{}).Where("id = ?", task.Id).
		Update("required_skill", uniq).Error; err != nil {
		t.Fatalf("设置技能需求失败: %v", err)
	}

	resp, err := logic.TaskAssign(&types.TaskAssignReq{Id: task.Id, AssigneeId: 0})
	if err != nil {
		t.Fatalf("自动指派失败: %v", err)
	}
	if resp.AssigneeId != staffId {
		t.Errorf("自动指派人 = %d(%s), 期望技能唯一命中的 %d",
			resp.AssigneeId, resp.AssigneeName, staffId)
	}
	if resp.AssigneeName != "自动指派命中者" {
		t.Errorf("处理人姓名 = %q, 期望从人员池反查得到", resp.AssigneeName)
	}
	if resp.Status != int32(model.StatusAssigned) {
		t.Errorf("状态 = %d, 期望已指派", resp.Status)
	}
}
