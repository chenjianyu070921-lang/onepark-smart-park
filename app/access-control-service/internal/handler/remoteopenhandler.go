package handler

import (
	"net/http"

	"onepark/app/access-control-service/internal/logic"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// RemoteOpenHandler 远程开门(docs/m3/04 #47): 统一响应体 {code,msg,data}.
func RemoteOpenHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.RemoteOpenReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAccessParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewRemoteOpenLogic(r.Context(), svcCtx)
		resp, err := l.RemoteOpen(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrAccessRemoteOpen, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
