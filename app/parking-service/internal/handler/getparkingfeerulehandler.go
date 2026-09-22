package handler

import (
	"net/http"

	"onepark/app/parking-service/internal/logic"
	"onepark/app/parking-service/internal/svc"
	"onepark/common/errorx"
	"onepark/common/response"
)

// GetParkingFeeRuleHandler 查询当前生效停车计费规则(P2 计费规则配置接口).
// 路由: GET /api/parking/fee-rule (对外经网关同前缀转发).
func GetParkingFeeRuleHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := logic.NewGetParkingFeeRuleLogic(r.Context(), svcCtx)
		resp, err := l.GetParkingFeeRule()
		if err != nil {
			// 业务错误码(如缺租户)透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
