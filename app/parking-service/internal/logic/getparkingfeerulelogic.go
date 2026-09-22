package logic

import (
	"context"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// GetParkingFeeRuleLogic 查询当前生效停车计费规则逻辑(P2 计费规则配置接口).
type GetParkingFeeRuleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewGetParkingFeeRuleLogic 构造查询计费规则逻辑.
func NewGetParkingFeeRuleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetParkingFeeRuleLogic {
	return &GetParkingFeeRuleLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// GetParkingFeeRule 查询当前生效计费规则; 无配置时返回 rule=nil(由离场计费逻辑降级为内置默认).
// 入参: 网关注入的 x-tenant-id.
// 返回: 当前生效规则项(免费时长/单价/封顶/生效时间窗), 无配置时为 null.
func (l *GetParkingFeeRuleLogic) GetParkingFeeRule() (*types.ParkingFeeRuleResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	rule, err := model.GetActiveParkingFeeRule(l.svcCtx.DB, tenantID, time.Now())
	if err != nil {
		l.Errorf("query parking fee rule failed: %v", err)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询计费规则失败")
	}
	resp := &types.ParkingFeeRuleResp{}
	if rule != nil {
		if cfg, e := rule.ParseRule(); e == nil {
			resp.Rule = &types.ParkingFeeRuleItem{
				Id:            rule.ID,
				FreeMinutes:   cfg.FreeMinutes,
				HourlyFee:     cfg.HourlyFee,
				DailyCap:      cfg.DailyCap,
				EffectiveFrom: unixOrZero(rule.EffectiveFrom),
				EffectiveTo:   unixOrZero(rule.EffectiveTo),
			}
		}
	}
	return resp, nil
}

// unixOrZero 秒级时间戳转 int64, nil 返回 0(计费规则时间窗展示用).
func unixOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
