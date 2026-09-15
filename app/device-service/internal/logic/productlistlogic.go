package logic

import (
	"context"
	"time"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type ProductListLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProductListLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProductListLogic {
	return &ProductListLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ProductListLogic) ProductList(req *types.ProductListReq) (resp *types.ProductListResp, err error) {
	page, size := normalizePage(req.Page, req.Size)

	list, total, err := l.svcCtx.ProductModel.FindList(l.ctx, page, size, int8(req.Status))
	if err != nil {
		l.Errorf("查询产品列表失败: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "查询产品列表失败")
	}

	items := make([]types.ProductListItem, 0, len(list))
	for _, p := range list {
		items = append(items, types.ProductListItem{
			ProductKey:  p.ProductKey,
			ProductName: p.ProductName,
			Status:      p.Status,
			CreatedAt:   p.CreatedAt.Format(time.DateTime),
		})
	}

	return &types.ProductListResp{Total: int(total), List: items}, nil
}
