package handler

import (
	"net/http"

	"onepark/app/notice-service/internal/logic"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListNoticesHandler 公告列表 HTTP 处理器.
// 路由: GET /api/notices (对外经网关: GET /api/notices).
// 职责: 解析查询串(type/status/page/page_size) -> 调 ListNoticesLogic 分页 -> 统一响应体.
func ListNoticesHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析查询参数(form); page/page_size 必填.
		var req types.ListNoticeReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 分页查询(租户隔离, 可选类型/状态过滤).
		l := logic.NewListNoticesLogic(r.Context(), svcCtx)
		resp, err := l.ListNotices(&req)
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
