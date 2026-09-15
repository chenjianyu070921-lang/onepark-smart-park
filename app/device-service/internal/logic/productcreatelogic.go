package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"onepark/app/device-service/internal/model"
	"onepark/app/device-service/internal/svc"
	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

type ProductCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewProductCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ProductCreateLogic {
	return &ProductCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ProductCreateLogic) ProductCreate(req *types.ProductCreateReq) error {
	req.ProductKey = strings.TrimSpace(req.ProductKey)
	req.ProductName = strings.TrimSpace(req.ProductName)
	if req.ProductKey == "" {
		return errorx.NewError(errorx.ErrDeviceParamInvalid, "productKey 不能为空")
	}
	if req.ProductName == "" {
		return errorx.NewError(errorx.ErrDeviceParamInvalid, "productName 不能为空")
	}

	// 物模型必须是合法 JSON
	if raw := strings.TrimSpace(req.ThingModel); raw != "" && !json.Valid([]byte(raw)) {
		return errorx.NewError(errorx.ErrDeviceParamInvalid, "thingModel 必须是合法 JSON 字符串")
	}

	// productKey 唯一性校验
	if _, err := l.svcCtx.ProductModel.FindByKey(l.ctx, req.ProductKey); err == nil {
		return errorx.NewError(errorx.ErrDeviceDuplicate, "productKey 已存在")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		l.Errorf("查询产品失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "查询产品失败")
	}

	p := &model.Product{
		ProductKey:  req.ProductKey,
		ProductName: req.ProductName,
		Description: req.Description,
		Status:      1,
	}
	if raw := strings.TrimSpace(req.ThingModel); raw != "" {
		p.ThingModel = datatypes.JSON(raw)
	}

	if err := l.svcCtx.ProductModel.Insert(l.ctx, p); err != nil {
		l.Errorf("产品写入失败: %v", err)
		return errorx.NewError(errorx.ErrInternal, "产品写入失败")
	}

	l.Infof("产品创建成功: productKey=%s", req.ProductKey)
	return nil
}
