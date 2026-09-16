// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package dispatch

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm/clause"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/errorx"
)

// staffUpdateColumns upsert 冲突时允许覆盖的列。
// 刻意不含 id / created_at(不可变), 也不含 updated_at(由 DDL 的 ON UPDATE CURRENT_TIMESTAMP 维护)。
var staffUpdateColumns = []string{"name", "phone", "zone_code", "skills", "on_duty", "status"}

// StaffUpsertLogic 新增/更新调度人员(技能池).
type StaffUpsertLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewStaffUpsertLogic 构造人员写入逻辑.
func NewStaffUpsertLogic(ctx context.Context, svcCtx *svc.ServiceContext) *StaffUpsertLogic {
	return &StaffUpsertLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// StaffUpsert 以 staff_id 为业务键做幂等写入: 已存在则更新, 不存在则新增.
//
// 为什么用 upsert 而不是要求调用方区分新增/修改:
// 前端维护人员名单时往往会整体提交一遍, 分开两个接口必然出现"重复新增"的脏数据。
func (l *StaffUpsertLogic) StaffUpsert(req *types.StaffUpsertReq) (*types.StaffUpsertResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.StaffId <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "staff_id 必须大于 0")
	}
	if req.Name == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "姓名不能为空")
	}
	// 常驻区域是就近指派的唯一依据, 缺了就只能靠负载兜底, 必须强制
	if req.ZoneCode == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "常驻区域不能为空, 就近指派依赖它")
	}
	if int8(req.OnDuty) != model.StaffOnDuty && int8(req.OnDuty) != model.StaffOffDuty {
		return nil, errorx.NewError(errorx.ErrBadRequest, "on_duty 仅支持 1(在岗) / 0(不在岗)")
	}
	if int8(req.Status) != model.StaffEnabled && int8(req.Status) != model.StaffDisabled {
		return nil, errorx.NewError(errorx.ErrBadRequest, "status 仅支持 1(启用) / 0(停用)")
	}

	staff := &model.DispatchStaff{
		StaffId:  req.StaffId,
		Name:     req.Name,
		Phone:    req.Phone,
		ZoneCode: req.ZoneCode,
		// 归一化: 去空白/去空项/去重/小写, 否则 "Fire" 与 "fire" 匹配不上
		Skills: model.NormalizeSkills(req.Skills),
		OnDuty: int8(req.OnDuty),
		Status: int8(req.Status),
	}

	err := l.svcCtx.DB.WithContext(l.ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "staff_id"}},
		DoUpdates: clause.AssignmentColumns(staffUpdateColumns),
	}).Create(staff).Error
	if err != nil {
		l.Errorf("[dispatch] upsert staff failed: staffId=%d, err=%v", req.StaffId, err)
		return nil, errorx.NewError(errorx.ErrInternal, "保存人员失败")
	}

	// MySQL 的 ON DUPLICATE KEY UPDATE 不保证回填自增 ID, 因此按业务键回查一次
	var id int64
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.DispatchStaff{}).
		Select("id").Where("staff_id = ?", req.StaffId).Scan(&id).Error; err != nil {
		l.Errorf("[dispatch] reload staff id failed: staffId=%d, err=%v", req.StaffId, err)
		return nil, errorx.NewError(errorx.ErrInternal, "保存人员失败")
	}

	l.Infof("[dispatch] 人员已保存: staffId=%d, name=%s, zone=%s, skills=%s, onDuty=%d",
		req.StaffId, req.Name, req.ZoneCode, staff.Skills, req.OnDuty)

	return &types.StaffUpsertResp{Id: id}, nil
}
