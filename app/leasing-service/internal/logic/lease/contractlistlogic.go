package lease

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/ecode"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// maxPageSize 单页上限, 防止一次拉全库.
const maxPageSize = 200

// ContractListLogic 合同分页列表.
type ContractListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewContractListLogic 构造合同列表逻辑.
func NewContractListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ContractListLogic {
	return &ContractListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ContractList 按状态/园区分页查询合同.
func (l *ContractListLogic) ContractList(req *types.ContractListReq) (*types.ContractListResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > maxPageSize {
		req.PageSize = 10
	}

	// 用闭包构造查询条件, 避免 Count 与 Find 共用同一个被 GORM 改写的 Statement.
	// 租户维度以网关注入的 x-tenant-id 为准(ctxdata), 不信任客户端自报的 req.TenantId.
	tenantID := ctxdata.GetTenantId(l.ctx)
	scope := func() *gorm.DB {
		db := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseContract{}).
			Where("tenant_id = ?", tenantID)
		if req.Status != 0 {
			db = db.Where("status = ?", req.Status)
		}
		return db
	}

	var total int64
	if err := scope().Count(&total).Error; err != nil {
		l.Errorf("[lease] count contracts failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractListFailed, "查询合同列表失败")
	}

	records := make([]model.LeaseContract, 0, req.PageSize)
	if err := scope().
		Order("id DESC").
		Offset(int((req.Page - 1) * req.PageSize)).
		Limit(int(req.PageSize)).
		Find(&records).Error; err != nil {
		l.Errorf("[lease] page contracts failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractListFailed, "查询合同列表失败")
	}

	items := make([]types.Contract, 0, len(records))
	for i := range records {
		items = append(items, toContractDTO(&records[i]))
	}

	return &types.ContractListResp{Total: total, List: items}, nil
}
