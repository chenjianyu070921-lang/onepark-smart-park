package handler

import (
	"net/http"

	"onepark/app/parking-service/internal/logic"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// UpsertParkingFeeRuleHandler 保存(新增)停车计费规则(P2 计费规则配置接口).
// 路由: POST /api/parking/fee-rule (对外经网关同前缀转发).
func UpsertParkingFeeRuleHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpsertParkingFeeRuleReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "请求参数解析失败"))
			return
		}
		l := logic.NewUpsertParkingFeeRuleLogic(r.Context(), svcCtx)
		resp, err := l.UpsertParkingFeeRule(&req)
		if err != nil {
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
