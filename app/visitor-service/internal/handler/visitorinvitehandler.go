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

// VisitorInviteHandler 发起访客邀请 HTTP 处理器.
// 路由: POST /api/visitor/invite (对外经网关: POST /api/visitor/invite).
// 职责: 解析校验请求体 -> 调 VisitorInviteLogic(生成签名二维码+写记录) -> 统一响应体.
func VisitorInviteHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(访客姓名/手机号/到访时间/过期时间等).
		var req types.VisitorInviteReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行邀请: 生成带 md5 签名+有效期的加密二维码并落库(强制租户).
		l := logic.NewVisitorInviteLogic(r.Context(), svcCtx)
		resp, err := l.VisitorInvite(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出(如缺租户 M6-E-0001), 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回记录ID/加密二维码内容/过期时间.
		response.Ok(w, resp)
	}
}
