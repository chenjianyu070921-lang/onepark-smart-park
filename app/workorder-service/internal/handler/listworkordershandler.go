package handler

import (
	"net/http"

	"onepark/app/workorder-service/internal/logic"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListWorkOrdersHandler 工单列表 HTTP 处理器.
// 路由: GET /api/workorders (对外经网关: GET /api/workorders).
// 职责: 解析查询串(status/type/assignee_id/page/page_size) -> 调 ListWorkOrdersLogic 分页查询 -> 统一响应体.
func ListWorkOrdersHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析查询参数(form); page/page_size 为必填, 缺失返回 400.
		var req types.ListWorkOrderReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 分页查询(租户隔离 + 可选 status/type/assignee 过滤).
		l := logic.NewListWorkOrdersLogic(r.Context(), svcCtx)
		resp, err := l.ListWorkOrders(&req)
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
