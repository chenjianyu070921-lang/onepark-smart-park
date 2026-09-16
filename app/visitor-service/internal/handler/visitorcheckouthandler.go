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

// VisitorCheckoutHandler 访客签出 HTTP 处理器.
// 路由: POST /api/visitor/checkout (对外经网关: POST /api/visitor/checkout).
// 职责: 解析(记录ID 或 二维码) -> 调 VisitorCheckoutLogic 置已签出 -> 统一响应体.
func VisitorCheckoutHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析参数: id 与 qr_code 二选一(均为 optional).
		var req types.VisitorCheckoutReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行签出: 校验在签入状态后置已签出并写 checkout_at(租户隔离).
		l := logic.NewVisitorCheckoutLogic(r.Context(), svcCtx)
		resp, err := l.VisitorCheckout(&req)
		if err != nil {
			// 3) 失败: 记录不存在/状态非法等业务错误码透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回记录ID/状态/签出时间.
		response.Ok(w, resp)
	}
}
