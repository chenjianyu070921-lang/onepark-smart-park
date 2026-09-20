package logic

import (
	"context"

	"onepark/app/energy-agent-service/internal/ecode"
	"onepark/app/energy-agent-service/internal/model"
	"onepark/app/energy-agent-service/internal/svc"
	"onepark/app/energy-agent-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type ApproveLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewApproveLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ApproveLogic {
	return &ApproveLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Approve 审批建议。
//
// 目前只改状态: M6 的 workorder-service 还是空壳(.api 里只有 /from/:name 演示接口),
// 调不通, 所以转工单这一步先留空。等那边实现完, 在这里加一次 HTTP 调用就行,
// 改动只影响这一个函数, 不会扩散。
func (l *ApproveLogic) Approve(req *types.ApproveRequest) (*types.ApproveResponse, error) {
	if req.Id <= 0 {
		return nil, errorx.NewError(ecode.ErrSuggestionNotFound, "建议 id 不合法")
	}
	if req.Reviewer == "" {
		return nil, errorx.NewError(ecode.ErrSuggestionNotFound, "审批人不能为空")
	}

	sug, err := l.svcCtx.Agent.GetSuggestion(l.ctx, uint64(req.Id))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(ecode.ErrSuggestionNotFound, "这条建议不存在")
		}
		l.Logger.Errorf("查建议失败: %v", err)
		return nil, errorx.NewError(ecode.ErrQueryFailed, "查询建议失败")
	}

	// 已经审批过的不重复处理, 否则会把上次审批人和时间覆盖掉
	if sug.Status != model.SugStatusPending {
		return nil, errorx.NewError(ecode.ErrSuggestionReviewed, "这条建议已经审批过了")
	}

	if err := l.svcCtx.Agent.ApproveSuggestion(l.ctx, uint64(req.Id), req.Approved, req.Reviewer); err != nil {
		l.Logger.Errorf("审批失败: %v", err)
		return nil, errorx.NewError(ecode.ErrQueryFailed, "审批失败")
	}

	status := model.SugStatusRejected
	if req.Approved {
		status = model.SugStatusApproved
	}

	return &types.ApproveResponse{
		Id:     req.Id,
		Status: int64(status),
		// 工单号暂时为空: 等 M6 的工单服务可用后再填
		WorkorderNo: "",
	}, nil
}
