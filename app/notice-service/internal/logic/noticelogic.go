package logic

import (
	"context"

	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type NoticeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeLogic {
	return &NoticeLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *NoticeLogic) Notice(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
