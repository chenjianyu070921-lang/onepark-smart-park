package logic

import (
	"context"
	"encoding/json"

	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
)

// RuleListLogic 接口60: 计费规则列表
type RuleListLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRuleListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RuleListLogic {
	return &RuleListLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *RuleListLogic) RuleList(req *types.RuleListRequest) (*types.RuleListResponse, error) {
	status := StatusOf(req.Status)
	list, err := l.svcCtx.Billing.ListRule(l.ctx, req.ZoneId, status)
	if err != nil {
		return nil, wrapErr("查询计费规则", err)
	}

	items := make([]types.RuleItem, 0, len(list))
	for _, r := range list {
		var cfg types.RuleConfig
		// 某条规则的 JSON 坏了也别让整个列表挂掉, 解析失败就返回空配置
		_ = json.Unmarshal([]byte(r.ConfigJSON), &cfg)

		items = append(items, types.RuleItem{
			Id:        int64(r.ID),
			Name:      r.Name,
			ZoneId:    r.ZoneID,
			RuleType:  r.RuleType,
			Config:    cfg,
			Status:    r.Status,
			CreatedAt: r.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return &types.RuleListResponse{
		Total: int64(len(items)),
		List:  items,
	}, nil
}
