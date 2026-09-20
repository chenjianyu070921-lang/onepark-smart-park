package logic

import (
	"context"
	"time"

	"onepark/app/energy-agent-service/internal/ecode"
	"onepark/app/energy-agent-service/internal/model"
	"onepark/app/energy-agent-service/internal/rules"
	"onepark/app/energy-agent-service/internal/svc"
	"onepark/app/energy-agent-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type InspectLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewInspectLogic(ctx context.Context, svcCtx *svc.ServiceContext) *InspectLogic {
	return &InspectLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Inspect 手动触发一次巡检。
// 定时任务走的是同一个 RunInspect, 所以手动和自动产出的报告完全一致 ——
// 不会出现"手动点没事、定时跑就出问题"这种两套代码的情况。
func (l *InspectLogic) Inspect(req *types.InspectRequest) (*types.InspectResponse, error) {
	// 默认统计昨天: 今天的数据往往还没上报齐, 拿半天数据去比一整天必然误报
	statDate := time.Now().AddDate(0, 0, -1)
	if req.StatDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", req.StatDate, time.Local)
		if err != nil {
			return nil, errorx.NewError(ecode.ErrBadTimeRange, "日期格式不对, 要写成 2026-09-16 这样")
		}
		// 不允许查未来: 未来肯定没数据, 报出来全是"数据缺失"的噪音
		if parsed.After(time.Now()) {
			return nil, errorx.NewError(ecode.ErrBadTimeRange, "不能统计未来的日期")
		}
		statDate = parsed
	}

	res, err := RunInspect(l.ctx, l.svcCtx, InspectInput{
		ZoneID:     req.ZoneId,
		StatDate:   statDate,
		Trigger:    "manual",
		DisableLLM: req.DisableLLM,
	})
	if err != nil {
		// 区域没数据这类是业务错误, 保留它自己的错误码
		if ce, ok := err.(*errorx.CodeError); ok {
			return nil, ce
		}
		return nil, errorx.NewError(ecode.ErrInspectFailed, err.Error())
	}

	sugs := make([]types.SuggestionItem, 0, len(res.Findings))
	for _, f := range res.Findings {
		sugs = append(sugs, toSuggestionItem(f))
	}

	return &types.InspectResponse{
		RunNo:       res.RunNo,
		StatDate:    res.StatDate,
		ZoneCount:   int64(res.ZoneCount),
		FindCount:   int64(len(res.Findings)),
		LLMEnabled:  res.LLMEnabled,
		LLMModel:    res.LLMModel,
		CostMs:      int64(res.CostMs),
		Suggestions: sugs,
	}, nil
}

// toSuggestionItem 把规则层的发现转成接口返回结构。
// 巡检接口返回时建议刚落库、还没回查主键, 所以 Id 为 0,
// 要带 id 的走建议列表接口。
func toSuggestionItem(f rules.Finding) types.SuggestionItem {
	return types.SuggestionItem{
		ZoneId:     f.ZoneID,
		DeviceId:   f.DeviceID,
		Category:   f.Category,
		Severity:   int64(f.Severity),
		Title:      f.Title,
		Reason:     f.Reason,
		Confidence: f.Confidence,
		Action:     f.Action,
		Status:     int64(model.SugStatusPending),
	}
}
