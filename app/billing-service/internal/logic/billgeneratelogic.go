package logic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"onepark/common/errorx"

	"onepark/app/billing-service/internal/calc"
	"onepark/app/billing-service/internal/ecode"
	"onepark/app/billing-service/internal/model"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// BillGenerateLogic 接口61: 生成账单
type BillGenerateLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBillGenerateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillGenerateLogic {
	return &BillGenerateLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *BillGenerateLogic) BillGenerate(req *types.BillGenerateRequest) (*types.BillGenerateResponse, error) {
	// 1. 参数校验
	if strings.TrimSpace(req.ZoneId) == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "zoneId 不能为空")
	}
	start, end, ok := ParsePeriod(req.Period)
	if !ok {
		return nil, errorx.NewError(ecode.ErrBadTimeRange, "period 格式不对, 应该像 2026-09")
	}
	lastDay := LastDay(end)

	// 2. 先算这个区域这个月用了多少度
	usage, err := l.svcCtx.EnergyReading.TotalUsage(l.ctx, req.ZoneId, start, end)
	if err != nil {
		return nil, wrapErr("统计用量", err)
	}
	if usage <= 0 {
		return nil, errorx.NewError(ecode.ErrNoUsage,
			fmt.Sprintf("区域 %s 在 %s 没有用量数据, 出不了账", req.ZoneId, start.Format(timeLayoutMonth)))
	}

	// 3. 找用哪条规则算钱
	rule, err := l.pickRule(req)
	if err != nil {
		return nil, err
	}

	// 4. 算钱: 按规则类型走不同的算法
	amount, details, err := l.calculate(rule, req.ZoneId, usage, start, end)
	if err != nil {
		return nil, err
	}

	// 5. 同一个区域同一个账期只能出一次账, 出过了就报错(防止重复扣钱)
	if old, err := l.svcCtx.Billing.FindBillByPeriod(l.ctx, req.ZoneId, start, lastDay); err == nil {
		return nil, errorx.NewError(ecode.ErrBillExists,
			fmt.Sprintf("该账期已经出过账了, 账单号 %s", old.BillNo))
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, wrapErr("检查是否重复出账", err)
	}

	// 6. 落库: 同时存下规则快照和计费明细, 以后规则改了老账单也能对得上
	bill := &model.Bill{
		BillNo:       fmt.Sprintf("B%s-%s", start.Format("200601"), req.ZoneId),
		ZoneID:       req.ZoneId,
		RuleID:       rule.ID,
		RuleSnapshot: rule.ConfigJSON,
		Detail:       marshalJSON(details),
		PeriodStart:  start,
		PeriodEnd:    lastDay,
		UsageKwh:     round2(usage),
		Amount:       amount,
		Status:       model.BillStatusUnpaid,
	}
	if err := l.svcCtx.Billing.InsertBill(l.ctx, bill); err != nil {
		return nil, wrapErr("生成账单", err)
	}

	return &types.BillGenerateResponse{
		BillNo:      bill.BillNo,
		ZoneId:      bill.ZoneID,
		PeriodStart: start.Format(timeLayoutDate),
		PeriodEnd:   lastDay.Format(timeLayoutDate),
		RuleId:      int64(rule.ID),
		RuleName:    rule.Name,
		UsageKwh:    round2(usage),
		Amount:      amount,
		Detail:      toDetailItems(details),
	}, nil
}

// pickRule 挑规则: 指定了 ruleId 就用它, 没指定就自动找该区域能用的
func (l *BillGenerateLogic) pickRule(req *types.BillGenerateRequest) (*model.BillingRule, error) {
	if req.RuleId > 0 {
		rule, err := l.svcCtx.Billing.FindRule(l.ctx, uint64(req.RuleId))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errorx.NewError(ecode.ErrRuleNotFound,
					fmt.Sprintf("找不到 id=%d 的计费规则", req.RuleId))
			}
			return nil, wrapErr("查询计费规则", err)
		}
		return rule, nil
	}

	rule, err := l.svcCtx.Billing.FindApplicableRule(l.ctx, req.ZoneId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(ecode.ErrNoRuleMatch,
				fmt.Sprintf("区域 %s 没有启用的计费规则, 也没有全园区默认规则, 先去建一条", req.ZoneId))
		}
		return nil, wrapErr("查询适用规则", err)
	}
	return rule, nil
}

// calculate 按规则类型算钱, 同时返回计费明细
func (l *BillGenerateLogic) calculate(rule *model.BillingRule, zoneID string, usage float64,
	start, end time.Time) (float64, []calc.DetailItem, error) {
	var cfg types.RuleConfig
	if err := json.Unmarshal([]byte(rule.ConfigJSON), &cfg); err != nil {
		return 0, nil, errorx.NewError(ecode.ErrBadRuleConfig, "规则配置不是合法 JSON, 没法算钱")
	}

	switch rule.RuleType {
	case model.RuleTypeFlat:
		amount := calc.CalcFlat(usage, cfg.Price)
		return amount, []calc.DetailItem{{
			Name:   "单一电价",
			Usage:  round2(usage),
			Price:  cfg.Price,
			Amount: amount,
		}}, nil

	case model.RuleTypeTiered:
		tiers := make([]calc.Tier, 0, len(cfg.Tiers))
		for _, t := range cfg.Tiers {
			tiers = append(tiers, calc.Tier{UpTo: t.UpTo, Price: t.Price})
		}
		amount, details := calc.CalcTiered(usage, tiers)
		return amount, details, nil

	case model.RuleTypeTou:
		// 峰谷要先按钟点把用量拆开, 才能知道峰段/平段/谷段各用了多少
		hourly, err := l.svcCtx.EnergyReading.ListHourlyUsage(l.ctx, zoneID, start, end)
		if err != nil {
			return 0, nil, wrapErr("统计分时用量", err)
		}
		periods := make([]calc.Period, 0, len(cfg.Periods))
		for _, p := range cfg.Periods {
			periods = append(periods, calc.Period{Name: p.Name, From: p.From, To: p.To, Price: p.Price})
		}
		amount, details := calc.CalcTou(hourly, cfg.Base, periods)
		return amount, details, nil

	default:
		return 0, nil, errorx.NewError(ecode.ErrBadRuleConfig,
			fmt.Sprintf("规则 %s 的类型 %d 不认识", rule.Name, rule.RuleType))
	}
}

// toDetailItems 把算法返回的明细转成接口要的结构
func toDetailItems(list []calc.DetailItem) []types.BillDetailItem {
	items := make([]types.BillDetailItem, 0, len(list))
	for _, d := range list {
		items = append(items, types.BillDetailItem{
			Name:   d.Name,
			Usage:  d.Usage,
			Price:  d.Price,
			Amount: d.Amount,
		})
	}
	return items
}
