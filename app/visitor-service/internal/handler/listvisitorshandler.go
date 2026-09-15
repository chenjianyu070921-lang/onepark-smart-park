package handler

import (
	"net/http"

	"onepark/app/visitor-service/internal/logic"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListVisitorsHandler 访客记录列表 HTTP 处理器.
// 路由: GET /api/visitors (对外经网关: GET /api/visitors).
// 职责: 解析查询串(status/page/page_size) -> 调 ListVisitorsLogic 分页查询 -> 统一响应体.
func ListVisitorsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析查询参数(form); page/page_size 必填.
		var req types.ListVisitorReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 分页查询(强制 tenant_id 隔离, 可选 status 过滤).
		l := logic.NewListVisitorsLogic(r.Context(), svcCtx)
		resp, err := l.ListVisitors(&req)
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
