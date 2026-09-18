package handler

import (
	"net/http"

	"onepark/app/notice-service/internal/logic"
	"onepark/app/notice-service/internal/svc"
	"onepark/common/errorx"
	"onepark/common/response"
)

// NoticeUnreadCountHandler 站内信未读计数 HTTP 处理器.
// 路由: GET /api/notices/unread-count (对外经网关: GET /api/notices/unread-count).
func NoticeUnreadCountHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := logic.NewNoticeUnreadCountLogic(r.Context(), svcCtx)
		resp, err := l.NoticeUnreadCount()
		if err != nil {
			// 失败: 业务错误码透出, 其余归 M2 兜底.
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
