package logic

import (
	"context"

	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AlarmLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAlarmLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AlarmLogic {
	return &AlarmLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AlarmLogic) Alarm(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
