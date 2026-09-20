package logic

import (
	"context"
	"strconv"

	"onepark/app/energy-agent-service/internal/ecode"
	"onepark/app/energy-agent-service/internal/model"
	"onepark/app/energy-agent-service/internal/svc"
	"onepark/app/energy-agent-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type SuggestionListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewSuggestionListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SuggestionListLogic {
	return &SuggestionListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// SuggestionList 建议列表。默认按严重程度排序, 最该处理的排最前面
func (l *SuggestionListLogic) SuggestionList(req *types.SuggestionListRequest) (*types.SuggestionListResponse, error) {
	status := 0
	if req.Status != "" {
		n, err := strconv.Atoi(req.Status)
		if err != nil || n < 0 {
			return nil, errorx.NewError(ecode.ErrBadTimeRange, "status 要填数字: 1待审批 2已通过 3已驳回 4已转工单")
		}
		status = n
	}

	list, total, err := l.svcCtx.Agent.ListSuggestions(l.ctx, model.SuggestionFilter{
		ZoneID:   req.ZoneId,
		Category: req.Category,
		Status:   status,
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	})
	if err != nil {
		logx.Errorf("查建议列表失败: %v", err)
		return nil, errorx.NewError(ecode.ErrQueryFailed, "查询建议列表失败")
	}

	items := make([]types.SuggestionItem, 0, len(list))
	for _, s := range list {
		items = append(items, types.SuggestionItem{
			Id:         int64(s.ID),
			ZoneId:     s.ZoneID,
			DeviceId:   s.DeviceID,
			Category:   s.Category,
			Severity:   int64(s.Severity),
			Title:      s.Title,
			Reason:     s.Reason,
			Confidence: s.Confidence,
			Action:     s.Action,
			Status:     int64(s.Status),
		})
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	return &types.SuggestionListResponse{
		Total:    total,
		Page:     page,
		PageSize: size,
		List:     items,
	}, nil
}
