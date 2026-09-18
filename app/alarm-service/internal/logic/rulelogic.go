package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// CreateRuleLogic 创建告警规则(#34).
type CreateRuleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateRuleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateRuleLogic {
	return &CreateRuleLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *CreateRuleLogic) CreateRule(req *types.CreateRuleReq) (*types.CreateRuleResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Rules == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "规则存储未就绪(MySQL 未配置)")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "name 不能为空")
	}
	if strings.TrimSpace(req.EventType) == "" {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "event_type 不能为空")
	}
	if req.Level < model.AlarmLevelInfo || req.Level > model.AlarmLevelCritical {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "level 取值范围为 1(提示)~4(紧急)")
	}
	if !rule.IsValidRuleType(req.RuleType) {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "rule_type 仅支持 threshold/combination/time_window")
	}
	// 条件必须能被引擎解析: 创建期校验, 避免脏规则写库后被引擎静默跳过(#34 错误码 M3-E-1002).
	if _, err := rule.ParseSpec(req.Conditions); err != nil {
		return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "规则条件解析失败: "+err.Error())
	}
	if req.Status != model.RuleStatusDisabled && req.Status != model.RuleStatusEnabled {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "status 仅支持 0(禁用)/1(启用)")
	}
	if req.WindowSeconds < 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "window_seconds 不能为负数")
	}

	now := time.Now()
	r := &model.AlarmRule{
		Name:          name,
		DeviceID:      strings.TrimSpace(req.DeviceId),
		AreaID:        req.AreaId,
		EventType:     strings.TrimSpace(req.EventType),
		RuleType:      normalizeRuleType(req.RuleType),
		Conditions:    req.Conditions,
		WindowSeconds: req.WindowSeconds,
		Level:         req.Level,
		Status:        req.Status,
	}
	r.TenantID = tenantID
	r.CreatedAt = now
	r.UpdatedAt = now

	if err := l.svcCtx.Rules.Create(l.ctx, r); err != nil {
		l.Errorf("create rule failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "创建告警规则失败")
	}
	invalidateRuleCache(l.svcCtx, l.Logger, r.ID, "create")
	return &types.CreateRuleResp{Id: r.ID}, nil
}

// invalidateRuleCache 规则写库成功后主动失效引擎快照, 使新规则对下一条事件立即生效.
//
// 不失效的后果: 引擎快照有 30s TTL(ruleCacheTTL), 期间仍按旧规则判定。
// 其中"禁用一条正在误报的规则后它又报了 30 秒"最难排查 —— 现象与"规则没配好"无法区分。
//
// 引擎未初始化(MySQL 未配置)时静默跳过: 此时规则库本身不可用, 也没有快照可失效。
// 失效是纯内存操作且不会失败, 因此不改写对外结果 —— 规则已入库是事实, 不因缓存动作失败而回滚.
func invalidateRuleCache(svcCtx *svc.ServiceContext, log logx.Logger, ruleID int64, action string) {
	if svcCtx == nil || svcCtx.Engine == nil {
		return
	}
	svcCtx.Engine.Invalidate()
	log.Infof("alarm rule cache invalidated after %s rule_id=%d", action, ruleID)
}

// normalizeRuleType 归一化规则类型名(兼容 docs/m3/04 的 composite/window 写法).
func normalizeRuleType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case rule.RuleTypeCombination, "composite":
		return rule.RuleTypeCombination
	case rule.RuleTypeTimeWindow, "window":
		return rule.RuleTypeTimeWindow
	default:
		return rule.RuleTypeThreshold
	}
}

// UpdateRuleLogic 更新 / 启用禁用规则(#35): 仅更新请求中显式传入的字段.
type UpdateRuleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateRuleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateRuleLogic {
	return &UpdateRuleLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *UpdateRuleLogic) UpdateRule(req *types.UpdateRuleReq) (*types.UpdateRuleResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "规则ID非法")
	}
	if l.svcCtx.Rules == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "规则存储未就绪(MySQL 未配置)")
	}

	updates := map[string]interface{}{}
	if name := strings.TrimSpace(req.Name); name != "" {
		updates["name"] = name
	}
	if deviceID := strings.TrimSpace(req.DeviceId); deviceID != "" {
		updates["device_id"] = deviceID
	}
	if req.AreaId != 0 {
		updates["area_id"] = req.AreaId
	}
	if eventType := strings.TrimSpace(req.EventType); eventType != "" {
		updates["event_type"] = eventType
	}
	// Level/Status 为指针: 未传(nil)与传 0(status=0 即禁用)必须区分开.
	if req.Level != nil {
		if *req.Level < model.AlarmLevelInfo || *req.Level > model.AlarmLevelCritical {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "level 取值范围为 1(提示)~4(紧急)")
		}
		updates["level"] = *req.Level
	}
	if req.Status != nil {
		if *req.Status != model.RuleStatusDisabled && *req.Status != model.RuleStatusEnabled {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "status 仅支持 0(禁用)/1(启用)")
		}
		updates["status"] = *req.Status
	}
	if req.RuleType != "" {
		if !rule.IsValidRuleType(req.RuleType) {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "rule_type 仅支持 threshold/combination/time_window")
		}
		updates["rule_type"] = normalizeRuleType(req.RuleType)
	}
	if req.Conditions != "" {
		if _, err := rule.ParseSpec(req.Conditions); err != nil {
			return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "规则条件解析失败: "+err.Error())
		}
		updates["conditions"] = req.Conditions
	}
	if req.WindowSeconds != 0 {
		if req.WindowSeconds < 0 {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "window_seconds 不能为负数")
		}
		updates["window_seconds"] = req.WindowSeconds
	}
	if len(updates) == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "没有需要更新的字段")
	}

	if err := l.svcCtx.Rules.Update(l.ctx, tenantID, req.Id, updates); err != nil {
		if err == model.ErrRuleNotFound {
			return nil, errorx.NewError(errorx.ErrAlarmRuleNotFound, "规则不存在")
		}
		l.Errorf("update rule failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "更新告警规则失败")
	}
	// 含"禁用规则"(status=0): 这条最需要立即生效, 否则被禁用的规则还会继续产生告警到 TTL 结束.
	invalidateRuleCache(l.svcCtx, l.Logger, req.Id, "update")
	return &types.UpdateRuleResp{Id: req.Id}, nil
}

// GetRuleLogic 规则详情(#36).
type GetRuleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetRuleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetRuleLogic {
	return &GetRuleLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *GetRuleLogic) GetRule(req *types.IdReq) (*types.RuleDetailResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Rules == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "规则存储未就绪(MySQL 未配置)")
	}

	r, err := l.svcCtx.Rules.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrRuleNotFound {
			return nil, errorx.NewError(errorx.ErrAlarmRuleNotFound, "规则不存在")
		}
		l.Errorf("get rule failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询规则详情失败")
	}
	return &types.RuleDetailResp{RuleItem: toRuleItem(r)}, nil
}

// ListRulesLogic 规则分页列表(#37).
type ListRulesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListRulesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListRulesLogic {
	return &ListRulesLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *ListRulesLogic) ListRules(req *types.ListRulesReq) (*types.ListRulesResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Rules == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "规则存储未就绪(MySQL 未配置)")
	}

	f := model.AlarmRuleListFilter{
		TenantID:  tenantID,
		DeviceID:  strings.TrimSpace(req.DeviceId),
		EventType: strings.TrimSpace(req.EventType),
		Page:      int(req.Page),
		PageSize:  int(req.PageSize),
	}
	// status 不传时 int8 零值与"禁用"同义, 约定: 负数表示不筛选.
	if req.Status == model.RuleStatusDisabled || req.Status == model.RuleStatusEnabled {
		status := req.Status
		f.Status = &status
	} else if req.Status >= 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "status 仅支持 0(禁用)/1(启用), 不筛选请传负数")
	}

	list, total, err := l.svcCtx.Rules.List(l.ctx, f)
	if err != nil {
		l.Errorf("list rules failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询规则列表失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	items := make([]types.RuleItem, 0, len(list))
	for _, r := range list {
		items = append(items, toRuleItem(r))
	}
	return &types.ListRulesResp{Total: total, Page: page, PageSize: size, List: items}, nil
}

// toRuleItem 将规则记录转为响应项.
func toRuleItem(r *model.AlarmRule) types.RuleItem {
	return types.RuleItem{
		Id:            r.ID,
		Name:          r.Name,
		DeviceId:      r.DeviceID,
		AreaId:        r.AreaID,
		EventType:     r.EventType,
		Level:         r.Level,
		RuleType:      r.RuleType,
		Conditions:    r.Conditions,
		WindowSeconds: r.WindowSeconds,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt.Unix(),
		UpdatedAt:     r.UpdatedAt.Unix(),
	}
}
