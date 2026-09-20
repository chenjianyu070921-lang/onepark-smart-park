package logic

import (
	"context"
	"testing"

	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
)

// TestProductCreateParamInvalid 参数校验必须先于产品查重/写库失败,
// 因此 svcCtx 传 nil 也能验证: 非法入参一律返回 ErrDeviceParamInvalid.
func TestProductCreateParamInvalid(t *testing.T) {
	l := NewProductCreateLogic(context.Background(), nil)

	tests := []struct {
		name string
		req  *types.ProductCreateReq
	}{
		{"productKey 为空", &types.ProductCreateReq{ProductName: "地磁产品"}},
		{"productName 为空", &types.ProductCreateReq{ProductKey: "pk_geo"}},
		{"thingModel 不是合法 JSON", &types.ProductCreateReq{ProductKey: "pk_geo", ProductName: "地磁产品", ThingModel: "{bad"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.ProductCreate(tt.req)
			ce, ok := err.(*errorx.CodeError)
			if !ok {
				t.Fatalf("期望 *CodeError, got %T: %v", err, err)
			}
			if ce.Code != errorx.ErrDeviceParamInvalid {
				t.Fatalf("期望错误码 %s, got %s", errorx.ErrDeviceParamInvalid, ce.Code)
			}
		})
	}
}
