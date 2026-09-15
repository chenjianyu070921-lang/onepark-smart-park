package logic

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type ProductDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProductDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProductDetailLogic {
	return &ProductDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ProductDetailLogic) ProductDetail(req *types.ProductDetailReq) (resp *types.ProductDetailResp, err error) {
	p, err := l.svcCtx.ProductModel.FindByKey(l.ctx, req.ProductKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.NewError(errorx.ErrProductNotFound, "产品不存在")
		}
		l.Errorf("查询产品失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询产品失败")
	}

	resp = &types.ProductDetailResp{
		ProductKey:  p.ProductKey,
		ProductName: p.ProductName,
		Description: p.Description,
		Status:      p.Status,
		CreatedAt:   p.CreatedAt.Format(time.DateTime),
	}
	if len(p.ThingModel) > 0 {
		resp.ThingModel = string(p.ThingModel)
	}
	return resp, nil
}
