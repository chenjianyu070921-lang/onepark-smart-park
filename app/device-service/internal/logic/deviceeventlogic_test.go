package logic

import (
	"context"
	"testing"

	"onepark/app/device-service/internal/types"
	"onepark/common/errorx"
)

// TestDeviceEventParamInvalid 参数校验必须先于任何外部依赖(DB/Redis/Kafka)失败,
// 因此 svcCtx 传 nil 也能验证: 非法入参一律返回 ErrDeviceParamInvalid.
func TestDeviceEventParamInvalid(t *testing.T) {
	l := NewDeviceEventLogic(context.Background(), nil)

	tests := []struct {
		name string
		req  *types.DeviceEventReq
	}{
		{"deviceId 为空", &types.DeviceEventReq{EventType: "fire"}},
		{"eventType 为空", &types.DeviceEventReq{DeviceID: "dev-001"}},
		{"payload 不是合法 JSON", &types.DeviceEventReq{DeviceID: "dev-001", EventType: "fire", Payload: "{bad"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.DeviceEvent(tt.req)
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
