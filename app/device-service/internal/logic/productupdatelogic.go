package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type ProductUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProductUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProductUpdateLogic {
	return &ProductUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ProductUpdateLogic) ProductUpdate(req *types.ProductUpdateReq) error {
	if req.ProductKey == "" {
		return errorx.NewError(errorx.ErrDeviceParamInvalid, "productKey 不能为空")
	}
	if raw := strings.TrimSpace(req.ThingModel); raw != "" && !json.Valid([]byte(raw)) {
		return errorx.NewError(errorx.ErrDeviceParamInvalid, "thingModel 必须是合法 JSON 字符串")
	}

	// 先查后改: model.Update 使用 Save, 需基于完整实体更新
	p, err := l.svcCtx.ProductModel.FindByKey(l.ctx, req.ProductKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.NewError(errorx.ErrProductNotFound, "产品不存在")
		}
		l.Errorf("查询产品失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "查询产品失败")
	}

	if v := strings.TrimSpace(req.ProductName); v != "" {
		p.ProductName = v
	}
	if v := strings.TrimSpace(req.Description); v != "" {
		p.Description = v
	}
	if v := strings.TrimSpace(req.ThingModel); v != "" {
		p.ThingModel = datatypes.JSON(v)
	}

	if err := l.svcCtx.ProductModel.Update(l.ctx, p); err != nil {
		l.Errorf("产品更新失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "产品更新失败")
	}

	l.Infof("产品更新成功: productKey=%s", req.ProductKey)
	return nil
}
