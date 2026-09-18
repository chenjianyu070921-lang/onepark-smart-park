package lease

import (
	"context"
	"errors"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/ecode"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ContractDetailLogic 查询合同详情.
type ContractDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewContractDetailLogic 构造合同详情逻辑.
func NewContractDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ContractDetailLogic {
	return &ContractDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ContractDetail 按主键查询合同.
func (l *ContractDetailLogic) ContractDetail(req *types.ContractDetailReq) (*types.ContractDetailResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	var contract model.LeaseContract
	err := l.svcCtx.DB.WithContext(l.ctx).
		Where("id = ? AND tenant_id = ?", req.Id, ctxdata.GetTenantId(l.ctx)).
		First(&contract).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(ecode.ErrContractNotFound, "合同不存在")
	}
	if err != nil {
		l.Errorf("[lease] query contract failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractQueryFailed, "查询合同失败")
	}

	return &types.ContractDetailResp{Contract: toContractDTO(&contract)}, nil
}
