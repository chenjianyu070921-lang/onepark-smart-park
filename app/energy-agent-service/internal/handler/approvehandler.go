package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"
	"onepark/common/errorx"
	"onepark/common/response"

	"onepark/app/energy-agent-service/internal/logic"
	"onepark/app/energy-agent-service/internal/svc"
	"onepark/app/energy-agent-service/internal/types"
)

// ApproveHandler 审批建议: POST /api/agent/suggestion/approve
func ApproveHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ApproveRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}

		l := logic.NewApproveLogic(r.Context(), svcCtx)
		resp, err := l.Approve(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		response.Ok(w, resp)
	}
}
