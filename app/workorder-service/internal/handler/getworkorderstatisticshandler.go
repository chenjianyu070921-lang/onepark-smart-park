package handler

import (
	"net/http"

	"onepark/app/workorder-service/internal/logic"
	"onepark/app/workorder-service/internal/svc"
	"onepark/common/errorx"
	"onepark/common/response"
)

// GetWorkOrderStatisticsHandler 工单统计看板(P2: 物业后台统计端点).
// 与 gRPC ListWorkOrders 共用同一聚合口径, 供 HTTP 管理端看板使用.
// 路由: GET /api/workorder/statistics (对外经网关同前缀转发).
func GetWorkOrderStatisticsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := logic.NewGetWorkOrderStatisticsLogic(r.Context(), svcCtx)
		resp, err := l.GetWorkOrderStatistics()
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
