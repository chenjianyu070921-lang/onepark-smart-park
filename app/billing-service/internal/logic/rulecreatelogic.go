package logic

import (
	"context"
	"strings"
	"time"

	"onepark/common/errorx"

	"onepark/app/billing-service/internal/ecode"
	"onepark/app/billing-service/internal/model"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// RuleCreateLogic 接口59: 创建计费规则
type RuleCreateLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRuleCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RuleCreateLogic {
	return &RuleCreateLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *RuleCreateLogic) RuleCreate(req *types.RuleCreateRequest) (*types.RuleCreateResponse, error) {
	// 1. 按规则类型校验配置, 不合法就不让存(免得出账时才发现算不出来)
	if err := validateRule(req); err != nil {
		return nil, err
	}

	// 2. 规则明细整块存成 JSON, 以后加字段不用改表
	rule := &model.BillingRule{
		Name:       req.Name,
		ZoneID:     req.ZoneId,
		RuleType:   req.RuleType,
		ConfigJSON: marshalJSON(req.Config),
		Status:     model.RuleStatusOn,
	}

	if err := l.svcCtx.Billing.InsertRule(l.ctx, rule); err != nil {
		return nil, wrapErr("创建计费规则", err)
	}

	return &types.RuleCreateResponse{Id: int64(rule.ID)}, nil
}

// validateRule 校验规则配置的合法性
func validateRule(req *types.RuleCreateRequest) error {
	if strings.TrimSpace(req.Name) == "" {
		return errorx.NewError(errorx.ErrBadRequest, "规则名称不能为空")
	}

	switch req.RuleType {
	case model.RuleTypeFlat:
		if req.Config.Price <= 0 {
			return errorx.NewError(ecode.ErrBadRuleConfig, "单一电价(ruleType=1)必须填 config.price 且大于 0")
		}

	case model.RuleTypeTiered:
		if len(req.Config.Tiers) == 0 {
			return errorx.NewError(ecode.ErrBadRuleConfig, "阶梯电价(ruleType=2)至少要配一档 config.tiers")
		}
		return validateTiers(req.Config.Tiers)

	case model.RuleTypeTou:
		if len(req.Config.Periods) == 0 && req.Config.Base <= 0 {
			return errorx.NewError(ecode.ErrBadRuleConfig,
				"峰谷电价(ruleType=3)至少要配一个 config.periods 时段, 或者填平段电价 config.base")
		}
		return validatePeriods(req.Config.Periods)

	default:
		return errorx.NewError(ecode.ErrBadRuleConfig, "ruleType 只能是 1(单一) / 2(阶梯) / 3(峰谷)")
	}
	return nil
}

// validateTiers 校验阶梯档位: 单价非负、上限递增、只有最后一档能不封顶
func validateTiers(tiers []types.TierItem) error {
	var prev float64
	for i, t := range tiers {
		if t.Price < 0 {
			return errorx.NewError(ecode.ErrBadRuleConfig, "阶梯单价不能是负数")
		}
		if t.UpTo == nil {
			if i != len(tiers)-1 {
				return errorx.NewError(ecode.ErrBadRuleConfig, "只有最后一档可以不封顶(upTo 填 null)")
			}
			continue
		}
		if *t.UpTo <= prev {
			return errorx.NewError(ecode.ErrBadRuleConfig, "阶梯档位的上限必须一档比一档大")
		}
		prev = *t.UpTo
	}
	return nil
}

// validatePeriods 校验峰谷时段的时间格式, 必须是 "HH:MM"
func validatePeriods(periods []types.PeriodItem) error {
	for _, p := range periods {
		if _, err := time.Parse("15:04", p.From); err != nil {
			return errorx.NewError(ecode.ErrBadRuleConfig, "时段起始时间格式不对, 应该像 08:00")
		}
		if _, err := time.Parse("15:04", p.To); err != nil {
			return errorx.NewError(ecode.ErrBadRuleConfig, "时段结束时间格式不对, 应该像 11:00")
		}
		if p.Price < 0 {
			return errorx.NewError(ecode.ErrBadRuleConfig, "时段单价不能是负数")
		}
	}
	return nil
}
