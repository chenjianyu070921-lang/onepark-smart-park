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

// AccessRemoteOpenHandler 远程开门 HTTP 处理器.
// 路由: POST /api/access/remote-open (对外经网关: POST /api/access/remote-open).
// 职责: 解析点位ID -> 调 AccessRemoteOpenLogic(查点位 + 调 M1 SendCommand + 落通行记录) -> 统一响应体.
func AccessRemoteOpenHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(gate_id 必填).
		var req types.AccessRemoteOpenReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行远程开门: 点位校验 -> M1 SendCommand(短超时) -> 通行记录落库(成功/失败均落).
		l := logic.NewAccessRemoteOpenLogic(r.Context(), svcCtx)
		resp, err := l.AccessRemoteOpen(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出(M6-E-0001/0003/0004, M3-E-2003), 其余归 M6-E-0005 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrInternal, err.Error())
			}
			return
		}

		// 4) 成功: 返回通行记录ID/点位ID/实际执行设备ID.
		response.Ok(w, resp)
	}
}
