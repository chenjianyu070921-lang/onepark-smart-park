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

// VisitorCheckinHandler 访客签入: 统一响应体 {code,msg,data}.
func VisitorCheckinHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.VisitorCheckinReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewVisitorCheckinLogic(r.Context(), svcCtx)
		resp, err := l.VisitorCheckin(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
