package kb

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/kb-service/internal/ecode"
	"onepark/app/kb-service/internal/model"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// FeedbackLogic 问答日志与反馈.
type FeedbackLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewFeedbackLogic(ctx context.Context, svcCtx *svc.ServiceContext) *FeedbackLogic {
	return &FeedbackLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// Logs 问答日志列表(审计 / 效果评估的数据源).
func (l *FeedbackLogic) Logs(req *types.ListQaLogsRequest) (*types.ListQaLogsResponse, error) {
	page, size := normalizePage(req.Page, req.PageSize)
	tenantID := uint64(ctxdata.GetTenantId(l.ctx))
	rows, total, err := l.svcCtx.KB.ListQaLogs(l.ctx, tenantID, page, size)
	if err != nil {
		return nil, errorx.NewError(ecode.ErrQaLogFailed, err.Error())
	}
	list := make([]types.QaLog, 0, len(rows))
	for _, r := range rows {
		list = append(list, types.QaLog{
			Id: int64(r.ID), Question: r.Question, Answer: r.Answer, Mode: r.Mode,
			TopScore: r.TopScore, ElapsedMs: r.ElapsedMs, CreatedAt: r.CreatedAt.Format(time.DateTime),
		})
	}
	return &types.ListQaLogsResponse{Total: total, List: list}, nil
}

// Feedback 对某条问答投票.
func (l *FeedbackLogic) Feedback(req *types.FeedbackRequest) (*types.FeedbackResponse, error) {
	tenantID := uint64(ctxdata.GetTenantId(l.ctx))
	if req.Vote != 1 && req.Vote != 2 {
		return nil, errorx.NewError(ecode.ErrFeedbackFailed, "vote 只能是 1(有用) 或 2(没用)")
	}
	// 先确认这条问答属于当前租户 —— 否则就是给别人的问答投票(越权)
	ok, err := l.svcCtx.KB.QaExists(l.ctx, tenantID, uint64(req.QaId))
	if err != nil {
		return nil, errorx.NewError(ecode.ErrFeedbackFailed, err.Error())
	}
	if !ok {
		return nil, errorx.NewError(ecode.ErrQaNotFound, "问答记录不存在")
	}
	err = l.svcCtx.KB.InsertFeedback(l.ctx, &model.KbFeedback{
		TenantID: tenantID, QaID: uint64(req.QaId), Vote: req.Vote, Comment: truncate(req.Comment, 500),
	})
	if err != nil {
		return nil, errorx.NewError(ecode.ErrFeedbackFailed, err.Error())
	}
	return &types.FeedbackResponse{Ok: true}, nil
}
