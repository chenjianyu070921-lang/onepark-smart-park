package logic

import (
	"context"

	"onepark/app/shadow-service/internal/svc"
	"onepark/proto/shadow"

	"github.com/zeromicro/go-zero/core/logx"
)

type PingLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewPingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PingLogic {
	return &PingLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *PingLogic) Ping(in *shadow.Request) (*shadow.Response, error) {
	// 回显请求内容作为 pong, 调用方可用任意 payload 验证链路连通
	return &shadow.Response{Pong: in.GetPing()}, nil
}
