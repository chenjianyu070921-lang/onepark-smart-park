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

// ActiveParkingHandler 在场车辆列表 HTTP 处理器.
// 路由: GET /api/parking/active (对外经网关: GET /api/parking/active).
// 职责: 解析分页参数 -> 调 ActiveParkingLogic 查 status=停车中 的记录 -> 统一响应体.
func ActiveParkingHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析分页参数(form); page/page_size 必填.
		var req types.ActiveParkingReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 查询在场车辆(租户隔离, 仅取 status=停车中).
		l := logic.NewActiveParkingLogic(r.Context(), svcCtx)
		resp, err := l.ActiveParking(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回 {total, list}.
		response.Ok(w, resp)
	}
}
