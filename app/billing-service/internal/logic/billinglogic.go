package logic

import (
	"context"

	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type BillingLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewBillingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BillingLogic {
	return &BillingLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *BillingLogic) Billing(req *types.Request) (resp *types.Response, err error) {
	// todo: add your logic here and delete this line

	return
}
