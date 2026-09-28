package logic

import (
	"context"
	"fmt"
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

// maxDeviceTypeLen 设备类型列宽(alarm_rule.device_type VARCHAR(32)).
// 入库前校验: 超长会让 MySQL 在严格模式下直接报错, 而错误文案不会指出是哪个字段.
const maxDeviceTypeLen = 32

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
	spec, err := rule.ParseSpec(req.Conditions)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "规则条件解析失败: "+err.Error())
	}
	// window_seconds 列此前是"死列"(只写不读), 2026-09-28 起引擎真正读取它, 校验必须同步跟上.
	if err := checkWindowSeconds(normalizeRuleType(req.RuleType), spec, req.WindowSeconds); err != nil {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, err.Error())
	}
	if req.Status != model.RuleStatusDisabled && req.Status != model.RuleStatusEnabled {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "status 仅支持 0(禁用)/1(启用)")
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if len(deviceType) > maxDeviceTypeLen {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid,
			fmt.Sprintf("device_type 长度不能超过 %d", maxDeviceTypeLen))
	}

	now := time.Now()
	r := &model.AlarmRule{
		Name:          name,
		DeviceType:    deviceType,
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

// checkWindowSeconds 校验时间窗口规则是否给出了窗口长度.
//
// 为什么要拦: 窗口长度两处都为空时, 引擎会静默落到 60s 默认窗口(engine.go#Evaluate)。
// 运维在控制台把规则设成 time_window 却忘了填窗口, 看到的是"规则在跑、告警在来",
// 只是阈值从"5 分钟 3 次"变成了"1 分钟 3 次" —— 误报量与排查难度都不成比例。
// 宁可创建期报 400 让它填, 也不要留下一条"看起来配好了其实没配"的规则。
//
// declaredType 为归一化后的规则类型(未传则空); spec 为条件解析结果(未传条件则 nil);
// windowSeconds 为列值(未传则 0)。三者都没提供时不校验 —— 增量更新里无法判断现存值。
func checkWindowSeconds(declaredType string, spec *rule.NormalizedSpec, windowSeconds int) error {
	if windowSeconds < 0 {
		return fmt.Errorf("window_seconds 不能为负数")
	}
	isWindow := declaredType == rule.RuleTypeTimeWindow ||
		(spec != nil && spec.Type == rule.RuleTypeTimeWindow)
	if !isWindow {
		return nil
	}
	if windowSeconds > 0 {
		return nil
	}
	if spec != nil && spec.WindowSec > 0 {
		return nil
	}
	return fmt.Errorf("time_window 规则必须指定窗口长度: 传 window_seconds, 或在 conditions 中声明 window_sec")
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
	if deviceType := strings.TrimSpace(req.DeviceType); deviceType != "" {
		if len(deviceType) > maxDeviceTypeLen {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid,
				fmt.Sprintf("device_type 长度不能超过 %d", maxDeviceTypeLen))
		}
		// 传空串表示"清空限定(不限设备类型)"? 不: 空串与"未传"在 string 上无法区分,
		// 与 device_id 保持一致 —— 置空请改用显式约定值, 避免误清空把规则放大到全部设备.
		updates["device_type"] = deviceType
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
	// declaredType / parsedSpec 供窗口长度校验使用: 增量更新下三者都可能缺失,
	// 故只在"本次请求确实把规则变成了时间窗口"时才校验(见 checkWindowSeconds).
	declaredType := ""
	if req.RuleType != "" {
		if !rule.IsValidRuleType(req.RuleType) {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "rule_type 仅支持 threshold/combination/time_window")
		}
		declaredType = normalizeRuleType(req.RuleType)
		updates["rule_type"] = declaredType
	}
	var parsedSpec *rule.NormalizedSpec
	if req.Conditions != "" {
		spec, err := rule.ParseSpec(req.Conditions)
		if err != nil {
			return nil, errorx.NewError(errorx.ErrAlarmRuleCreate, "规则条件解析失败: "+err.Error())
		}
		parsedSpec = spec
		updates["conditions"] = req.Conditions
	}
	if err := checkWindowSeconds(declaredType, parsedSpec, req.WindowSeconds); err != nil {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, err.Error())
	}
	if req.WindowSeconds != 0 {
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
		TenantID:   tenantID,
		DeviceType: strings.TrimSpace(req.DeviceType),
		DeviceID:   strings.TrimSpace(req.DeviceId),
		EventType:  strings.TrimSpace(req.EventType),
		Page:       int(req.Page),
		PageSize:   int(req.PageSize),
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
		DeviceType:    r.DeviceType,
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
