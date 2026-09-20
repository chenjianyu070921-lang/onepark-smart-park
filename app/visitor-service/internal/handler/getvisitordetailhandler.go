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

// GetVisitorDetailHandler 访客详情+进出轨迹(P2): 统一响应体 {code,msg,data}.
func GetVisitorDetailHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.VisitorIdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "请求参数解析失败"))
			return
		}

		l := logic.NewGetVisitorDetailLogic(r.Context(), svcCtx)
		resp, err := l.GetVisitorDetail(&req)
		if err != nil {
			// 失败: 记录不存在等业务错误码透出, 其余归 M2 兜底.
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
