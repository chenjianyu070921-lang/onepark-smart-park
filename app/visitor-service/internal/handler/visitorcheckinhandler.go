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

// VisitorCheckinHandler 访客扫码签入 HTTP 处理器.
// 路由: POST /api/visitor/checkin (对外经网关: POST /api/visitor/checkin).
// 职责: 解析二维码 -> 调 VisitorCheckinLogic(核销+调 M1 开门+回填 device_id) -> 统一响应体.
func VisitorCheckinHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(二维码内容 qr_code).
		var req types.VisitorCheckinReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行签入: 有效期/重复核销校验 -> 置已签入 -> gRPC 调 M1 SendCommand 开门(短超时, 失败降级).
		l := logic.NewVisitorCheckinLogic(r.Context(), svcCtx)
		resp, err := l.VisitorCheckin(&req)
		if err != nil {
			// 3) 失败: 二维码失效/已用等业务错误码透出(M2-E-2001/2002), 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回记录ID/状态/签入时间/开门设备ID.
		response.Ok(w, resp)
	}
}
