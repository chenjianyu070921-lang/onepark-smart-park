package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/video-service/internal/model"
	"onepark/app/video-service/internal/svc"
	"onepark/app/video-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// 录像计划参数的边界常量.
const (
	// defaultRetentionDays 未配置/未指定时的默认保留天数.
	defaultRetentionDays = 7
	// maxRetentionDays 保留天数上限: 再多就是"永不清理", 应当显式配置存储策略而不是留个巨大数值.
	maxRetentionDays = 3650
	// maxPlanNameLen 计划名称长度上限, 与 record_plan.name 的 varchar(64) 一致.
	maxPlanNameLen = 64
)

// planFields 归一化后的计划字段.
type planFields struct {
	strategy      string
	daysOfWeek    string
	startMinute   int
	endMinute     int
	retentionDays int
}

// CreateRecordPlanLogic 创建录像计划.
type CreateRecordPlanLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateRecordPlanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateRecordPlanLogic {
	return &CreateRecordPlanLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *CreateRecordPlanLogic) CreateRecordPlan(req *types.CreateRecordPlanReq) (*types.CreateRecordPlanResp, error) {
	tenantID, err := planGuard(l.ctx, l.svcCtx, req.CameraId)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "name 不能为空")
	}
	if len([]rune(name)) > maxPlanNameLen {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "name 长度不能超过 64")
	}
	fields, err := normalizePlanFields(req.Strategy, req.DaysOfWeek, req.StartMinute, req.EndMinute,
		req.RetentionDays, l.svcCtx.Config.Record.DefaultRetentionDays)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	p := &model.RecordPlan{
		Name:          name,
		CameraID:      req.CameraId,
		Strategy:      fields.strategy,
		DaysOfWeek:    fields.daysOfWeek,
		StartMinute:   fields.startMinute,
		EndMinute:     fields.endMinute,
		RetentionDays: fields.retentionDays,
		Status:        model.RecordPlanStatusEnabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	p.TenantID = tenantID
	// Status 用指针入参: 未传(新建启用)与显式传 0(新建停用)必须是两种结果,
	// 用零值判断会让"新建即停用"这条路径永远建不出来.
	if req.Status != nil {
		if err := validatePlanStatus(*req.Status); err != nil {
			return nil, err
		}
		p.Status = *req.Status
	}

	if err := l.svcCtx.RecordPlans.Create(l.ctx, p); err != nil {
		if err == model.ErrRecordPlanDuplicate {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "该摄像头下已存在同名录像计划")
		}
		l.Errorf("create record plan failed: %v", err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "创建录像计划失败")
	}
	return &types.CreateRecordPlanResp{Id: p.ID}, nil
}

// ListRecordPlansLogic 录像计划列表.
type ListRecordPlansLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListRecordPlansLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListRecordPlansLogic {
	return &ListRecordPlansLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *ListRecordPlansLogic) ListRecordPlans(req *types.ListRecordPlansReq) (*types.ListRecordPlansResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.RecordPlans == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "录像计划存储未就绪(MySQL 未配置)")
	}

	f := model.RecordPlanListFilter{TenantID: tenantID, CameraID: req.CameraId, Page: int(req.Page), PageSize: int(req.PageSize)}
	// status=0(停用)必须是可筛选的, 负数表示不筛选(与摄像头列表同一约定).
	if req.Status >= 0 {
		if err := validatePlanStatus(req.Status); err != nil {
			return nil, err
		}
		status := req.Status
		f.Status = &status
	}

	list, total, err := l.svcCtx.RecordPlans.List(l.ctx, f)
	if err != nil {
		l.Errorf("list record plans failed: %v", err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "查询录像计划列表失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	items := make([]types.RecordPlanItem, 0, len(list))
	for _, p := range list {
		items = append(items, toPlanItem(p))
	}
	return &types.ListRecordPlansResp{Total: total, Page: page, PageSize: size, List: items}, nil
}

// GetRecordPlanLogic 录像计划详情.
type GetRecordPlanLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetRecordPlanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetRecordPlanLogic {
	return &GetRecordPlanLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *GetRecordPlanLogic) GetRecordPlan(req *types.IdReq) (*types.RecordPlanItem, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "id 非法")
	}
	if l.svcCtx.RecordPlans == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "录像计划存储未就绪(MySQL 未配置)")
	}
	p, err := l.svcCtx.RecordPlans.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrRecordPlanNotFound {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanNotFound, "录像计划不存在")
		}
		l.Errorf("find record plan failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "查询录像计划失败")
	}
	item := toPlanItem(p)
	return &item, nil
}

// UpdateRecordPlanLogic 修改录像计划.
type UpdateRecordPlanLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateRecordPlanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateRecordPlanLogic {
	return &UpdateRecordPlanLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UpdateRecordPlanLogic) UpdateRecordPlan(req *types.UpdateRecordPlanReq) (*types.UpdateRecordPlanResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "id 非法")
	}
	if l.svcCtx.RecordPlans == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "录像计划存储未就绪(MySQL 未配置)")
	}

	// 修改是增量的, 但 scheduled 的三个字段必须一起看(缺一个就无法解释这条计划),
	// 因此先取出当前值, 在此基础上叠加本次传入的字段再整体校验.
	current, err := l.svcCtx.RecordPlans.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrRecordPlanNotFound {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanNotFound, "录像计划不存在")
		}
		l.Errorf("load record plan failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "查询录像计划失败")
	}

	patch := model.RecordPlanPatch{}
	if name := strings.TrimSpace(req.Name); name != "" {
		if len([]rune(name)) > maxPlanNameLen {
			return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "name 长度不能超过 64")
		}
		patch.Name = &name
	}

	strategy := current.Strategy
	if s := strings.TrimSpace(req.Strategy); s != "" {
		strategy = s
	}
	days := current.DaysOfWeek
	if req.DaysOfWeek != nil {
		days = *req.DaysOfWeek
	}
	startMinute := current.StartMinute
	if req.StartMinute != nil {
		startMinute = *req.StartMinute
	}
	endMinute := current.EndMinute
	if req.EndMinute != nil {
		endMinute = *req.EndMinute
	}
	// 策略改回 always 时把定时字段复位: 否则留下"全天策略 + 残留时段"这种自相矛盾的配置,
	// 回放推导会不知所措(看似全天, 实际读的是过期时段).
	if strategy == model.RecordStrategyAlways {
		days, startMinute, endMinute = "", 0, 0
	}
	retention := current.RetentionDays
	if req.RetentionDays != nil {
		retention = *req.RetentionDays
	}

	fields, err := normalizePlanFields(strategy, days, startMinute, endMinute, retention,
		l.svcCtx.Config.Record.DefaultRetentionDays)
	if err != nil {
		return nil, err
	}
	if fields.strategy != current.Strategy {
		patch.Strategy = &fields.strategy
	}
	if fields.daysOfWeek != current.DaysOfWeek {
		patch.DaysOfWeek = &fields.daysOfWeek
	}
	if fields.startMinute != current.StartMinute {
		patch.StartMinute = &fields.startMinute
	}
	if fields.endMinute != current.EndMinute {
		patch.EndMinute = &fields.endMinute
	}
	if fields.retentionDays != current.RetentionDays {
		patch.RetentionDays = &fields.retentionDays
	}
	if req.Status != nil {
		if err := validatePlanStatus(*req.Status); err != nil {
			return nil, err
		}
		patch.Status = req.Status
	}

	if err := l.svcCtx.RecordPlans.Update(l.ctx, tenantID, req.Id, patch); err != nil {
		if err == model.ErrRecordPlanDuplicate {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "该摄像头下已存在同名录像计划")
		}
		if err == model.ErrRecordPlanNotFound {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanNotFound, "录像计划不存在或未传入任何待修改字段")
		}
		l.Errorf("update record plan failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "修改录像计划失败")
	}
	return &types.UpdateRecordPlanResp{Id: req.Id}, nil
}

// DeleteRecordPlanLogic 删除录像计划.
// 删除只影响"接下来还录不录", 不影响已产生的录像(媒体在网关侧, 由保留期与网关策略决定).
type DeleteRecordPlanLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeleteRecordPlanLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteRecordPlanLogic {
	return &DeleteRecordPlanLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *DeleteRecordPlanLogic) DeleteRecordPlan(req *types.IdReq) (*types.DeleteRecordPlanResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrVideoParamInvalid, "id 非法")
	}
	if l.svcCtx.RecordPlans == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "录像计划存储未就绪(MySQL 未配置)")
	}
	if err := l.svcCtx.RecordPlans.Delete(l.ctx, tenantID, req.Id); err != nil {
		if err == model.ErrRecordPlanNotFound {
			return nil, errorx.NewError(errorx.ErrVideoRecordPlanNotFound, "录像计划不存在")
		}
		l.Errorf("delete record plan failed id=%d: %v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrVideoRecordPlanCreate, "删除录像计划失败")
	}
	return &types.DeleteRecordPlanResp{Id: req.Id}, nil
}

// planGuard 统一校验租户 / 摄像头主键 / 存储可用性.
//
// 为什么这里要连带检查 Cameras 而不是只查 plan 表: 录像计划必须挂在真实存在的摄像头上。
// 只查计划表时, 摄像头被删除后它的计划会成为孤儿 —— 列表看不见、回推导不出,
// 但数据还躺在库里, 下次有人注册同名摄像头时会被莫名继承.
func planGuard(ctx context.Context, svcCtx *svc.ServiceContext, cameraID int64) (int64, error) {
	tenantID := ctxdata.GetTenantId(ctx)
	if tenantID == 0 {
		return 0, errorx.NewError(errorx.ErrVideoParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if cameraID <= 0 {
		return 0, errorx.NewError(errorx.ErrVideoParamInvalid, "camera_id 非法")
	}
	if svcCtx.Cameras == nil || svcCtx.RecordPlans == nil {
		return 0, errorx.NewError(errorx.ErrDepConnect, "摄像头/录像计划存储未就绪(MySQL 未配置)")
	}
	if _, err := svcCtx.Cameras.FindByID(ctx, tenantID, cameraID); err != nil {
		if err == model.ErrCameraNotFound {
			return 0, errorx.NewError(errorx.ErrVideoCameraNotFound, "摄像头不存在")
		}
		return 0, errorx.NewError(errorx.ErrVideoStream, "查询摄像头失败")
	}
	return tenantID, nil
}

// normalizePlanFields 校验并归一化计划的策略相关字段.
//
// 关键取舍: scheduled 的三个字段(生效日/起/止)作为一个整体校验 ——
// 逐个字段单独合法但组合起来无解(如"定时但结束早于开始")是最常见的配置错误,
// 只在写这一侧拦住, 回放推导那边就不用再猜"这条计划到底什么意思".
//
// defaultRetention 为配置里的默认保留天数, 在未指定 retention_days 时兜底.
func normalizePlanFields(strategy, daysOfWeek string, startMinute, endMinute, retentionDays,
	defaultRetention int) (planFields, error) {
	strategy = strings.TrimSpace(strategy)
	if strategy == "" {
		strategy = model.RecordStrategyAlways
	}
	if strategy != model.RecordStrategyAlways && strategy != model.RecordStrategyScheduled {
		return planFields{}, errorx.NewError(errorx.ErrVideoParamInvalid,
			"strategy 仅支持 always(全天) / scheduled(定时)")
	}

	out := planFields{strategy: strategy, daysOfWeek: "", retentionDays: retentionDays}
	if strategy == model.RecordStrategyAlways {
		// 全天计划不带定时字段: 存即 merge 会让"策略=全天"却残留起止分钟, 语义自相矛盾.
		out.startMinute, out.endMinute = 0, 0
	} else {
		parsed, err := model.ParseDaysOfWeek(strings.TrimSpace(daysOfWeek))
		if err != nil {
			return planFields{}, errorx.NewError(errorx.ErrVideoParamInvalid, err.Error())
		}
		startMinute, endMinute = model.NormalizeMinutes(startMinute, endMinute)
		if endMinute <= startMinute {
			return planFields{}, errorx.NewError(errorx.ErrVideoParamInvalid,
				"end_minute 必须大于 start_minute; 跨零点请拆成两条计划")
		}
		// 归一后重新格式化: "2,1" 与 "1,2" 存成同一份文本, 列表不会被误读成两条配置.
		out.daysOfWeek = model.FormatDaysOfWeek(parsed)
		out.startMinute, out.endMinute = startMinute, endMinute
	}

	if out.retentionDays <= 0 {
		if defaultRetention > 0 {
			out.retentionDays = defaultRetention
		} else {
			out.retentionDays = defaultRetentionDays
		}
	}
	if out.retentionDays > maxRetentionDays {
		return planFields{}, errorx.NewError(errorx.ErrVideoParamInvalid, "retention_days 超出上限")
	}
	return out, nil
}

// validatePlanStatus 计划状态仅支持 0(停用)/1(启用).
func validatePlanStatus(status int8) error {
	if status != model.RecordPlanStatusDisabled && status != model.RecordPlanStatusEnabled {
		return errorx.NewError(errorx.ErrVideoParamInvalid, "status 仅支持 0(停用)/1(启用)")
	}
	return nil
}

// toPlanItem 数据模型转列表项.
func toPlanItem(p *model.RecordPlan) types.RecordPlanItem {
	return types.RecordPlanItem{
		Id:            p.ID,
		CameraId:      p.CameraID,
		Name:          p.Name,
		Strategy:      p.Strategy,
		DaysOfWeek:    p.DaysOfWeek,
		StartMinute:   p.StartMinute,
		EndMinute:     p.EndMinute,
		RetentionDays: p.RetentionDays,
		Status:        p.Status,
		CreatedAt:     p.CreatedAt.Unix(),
		UpdatedAt:     p.UpdatedAt.Unix(),
	}
}
