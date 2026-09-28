package logic

import (
	"context"
	"encoding/json"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/rbac"

	"github.com/zeromicro/go-zero/core/logx"
)

// UpsertParkingFeeRuleLogic 保存(新增)停车计费规则逻辑(P2 计费规则配置接口).
type UpsertParkingFeeRuleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewUpsertParkingFeeRuleLogic 构造保存计费规则逻辑.
func NewUpsertParkingFeeRuleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpsertParkingFeeRuleLogic {
	return &UpsertParkingFeeRuleLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// UpsertParkingFeeRule 保存一条计费规则(生效时间窗内的"最新一条"为当前生效规则).
// 入参: 免费时长(free_minutes)/每小时单价(hourly_fee, 必须>0)/每日封顶(daily_cap, 0=不封顶)/生效时间窗(可选).
// 返回: 保存后的规则项.
func (l *UpsertParkingFeeRuleLogic) UpsertParkingFeeRule(req *types.UpsertParkingFeeRuleReq) (*types.ParkingFeeRuleResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	// RBAC: 收费规则属停车管理员职责(设计文档第四章角色表), 与公告发布同口径做服务端兜底校验,
	// 防止任意同租户用户篡改计费规则(审查问题⑦).
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if !rbac.HasRole(roles, rbac.RoleSystemAdmin) && !rbac.HasRole(roles, rbac.RoleParkAdmin) && !rbac.HasRole(roles, rbac.RoleParkingAdmin) {
		return nil, errorx.NewError(errorx.ErrForbidden, "无权限配置计费规则")
	}
	if req.HourlyFee <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "每小时单价必须大于0")
	}
	// 数值边界校验: 免费时长/每日封顶不得为负 —— 负免费时长会使几乎所有停车都收费,
	// 负封顶会使"cfg.DailyCap > 0"恒不成立而等同"不封顶", 均与配置意图相反(审查问题⑦).
	if req.FreeMinutes < 0 {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "免费时长不能为负")
	}
	if req.DailyCap < 0 {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "每日封顶不能为负(0 表示不封顶)")
	}
	// 生效时间窗自洽: 两端都指定时, 生效止必须晚于生效起.
	if req.EffectiveFrom > 0 && req.EffectiveTo > 0 && req.EffectiveFrom >= req.EffectiveTo {
		return nil, errorx.NewError(errorx.ErrM2ParamInvalid, "生效止必须晚于生效起")
	}
	// 规则以 JSON 持久化, 便于后续扩展阶梯/分车型费率而不改表结构.
	cfg := model.ParkingFeeRuleConfig{FreeMinutes: req.FreeMinutes, HourlyFee: req.HourlyFee, DailyCap: req.DailyCap}
	ruleJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "计费规则序列化失败")
	}
	now := time.Now()
	rule := &model.ParkingFeeRule{
		RuleJSON:      string(ruleJSON),
		EffectiveFrom: unixPtrRule(req.EffectiveFrom),
		EffectiveTo:   unixPtrRule(req.EffectiveTo),
	}
	rule.TenantID = tenantID
	rule.CreatedAt = now
	rule.UpdatedAt = now
	if e := l.svcCtx.DB.WithContext(l.ctx).Create(rule).Error; e != nil {
		l.Errorf("save parking fee rule failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "保存计费规则失败")
	}
	resp := &types.ParkingFeeRuleResp{}
	if pc, e := rule.ParseRule(); e == nil {
		resp.Rule = &types.ParkingFeeRuleItem{
			Id:            rule.ID,
			FreeMinutes:   pc.FreeMinutes,
			HourlyFee:     pc.HourlyFee,
			DailyCap:      pc.DailyCap,
			EffectiveFrom: unixOrZero(rule.EffectiveFrom),
			EffectiveTo:   unixOrZero(rule.EffectiveTo),
		}
	}
	return resp, nil
}

// unixPtrRule 秒级时间戳转 *time.Time, 0/负数返回 nil(生效时间窗可选).
func unixPtrRule(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0)
	return &t
}
