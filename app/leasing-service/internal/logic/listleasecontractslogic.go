package logic

import (
	"context"

	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListLeaseContractsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListLeaseContractsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListLeaseContractsLogic {
	return &ListLeaseContractsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListLeaseContractsLogic) ListLeaseContracts(req *types.ListLeaseReq) (resp *types.ListLeaseResp, err error) {
	// todo: add your logic here and delete this line

	return
}
