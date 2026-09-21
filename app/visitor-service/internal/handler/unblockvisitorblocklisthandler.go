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

// UnblockVisitorBlocklistHandler 解除访客黑名单 HTTP 处理器.
// 路由: POST /api/visitor/blocklist/:id/unblock.
func UnblockVisitorBlocklistHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UnblockVisitorBlocklistReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		l := logic.NewUnblockVisitorBlocklistLogic(r.Context(), svcCtx)
		resp, err := l.UnblockVisitorBlocklist(req.Id)
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
