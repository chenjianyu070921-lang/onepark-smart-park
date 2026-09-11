package logic

import (
	"context"

	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type UsermanageLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUsermanageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UsermanageLogic {
	return &UsermanageLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UsermanageLogic) Usermanage(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
