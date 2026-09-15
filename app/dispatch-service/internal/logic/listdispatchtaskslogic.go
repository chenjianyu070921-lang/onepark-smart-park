package logic

import (
	"context"

	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListDispatchTasksLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListDispatchTasksLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListDispatchTasksLogic {
	return &ListDispatchTasksLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListDispatchTasksLogic) ListDispatchTasks(req *types.ListDispatchReq) (resp *types.ListDispatchResp, err error) {
	// todo: add your logic here and delete this line

	return
}
