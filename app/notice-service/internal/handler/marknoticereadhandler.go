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

// MarkNoticeReadHandler 公告已读回填 HTTP 处理器.
// 路由: POST /api/notices/read (对外经网关: POST /api/notices/read).
func MarkNoticeReadHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析请求体(notice_id).
		var req types.MarkNoticeReadReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "请求参数解析失败"))
			return
		}

		// 2) 回填已读(仅本人未读记录, 幂等).
		l := logic.NewMarkNoticeReadLogic(r.Context(), svcCtx)
		resp, err := l.MarkNoticeRead(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出, 其余归 M2 兜底.
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
