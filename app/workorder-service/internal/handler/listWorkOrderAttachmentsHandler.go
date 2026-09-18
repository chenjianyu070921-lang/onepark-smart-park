package handler

import (
	"net/http"

	"onepark/app/workorder-service/internal/logic"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListWorkOrderAttachmentsHandler 工单附件列表查询处理器.
// 路由: GET /api/workorder/:id/attachments.
func ListWorkOrderAttachmentsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListWorkOrderAttachmentsReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewListWorkOrderAttachmentsLogic(r.Context(), svcCtx)
		resp, err := l.ListWorkOrderAttachments(&req)
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
