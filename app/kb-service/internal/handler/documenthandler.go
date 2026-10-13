package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"onepark/app/kb-service/internal/logic/kb"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"
)

// CreateDocumentHandler POST /api/kb/documents
func CreateDocumentHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateDocumentRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewDocumentLogic(r.Context(), svcCtx).Create(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

// ListDocumentsHandler GET /api/kb/documents
func ListDocumentsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListDocumentsRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewDocumentLogic(r.Context(), svcCtx).List(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

// DeleteDocumentHandler DELETE /api/kb/documents/:id
func DeleteDocumentHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.DeleteDocumentRequest
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := kb.NewDocumentLogic(r.Context(), svcCtx).Delete(&req)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
