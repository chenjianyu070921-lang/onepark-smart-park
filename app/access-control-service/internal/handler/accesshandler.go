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

// GrantAccessHandler 门禁批量授权(docs/m3/04 #45): 统一响应体 {code,msg,data}.
func GrantAccessHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.GrantAccessReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAccessParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewGrantAccessLogic(r.Context(), svcCtx)
		resp, err := l.GrantAccess(&req)
		if err != nil {
			response.FailWith(w, errorx.ErrAccessGrant, err.Error())
			return
		}
		response.Ok(w, resp)
	}
}

// RevokeAccessHandler 门禁撤权(docs/m3/04 #46): 统一响应体 {code,msg,data}.
func RevokeAccessHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.RevokeAccessReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAccessParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewRevokeAccessLogic(r.Context(), svcCtx)
		resp, err := l.RevokeAccess(&req)
		if err != nil {
			response.FailWith(w, errorx.ErrAccessRevoke, err.Error())
			return
		}
		response.Ok(w, resp)
	}
}

// ListAccessRecordsHandler 通行记录查询(docs/m3/04 #48): 统一响应体 {code,msg,data}.
func ListAccessRecordsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListAccessRecordsReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAccessParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListAccessRecordsLogic(r.Context(), svcCtx)
		resp, err := l.ListAccessRecords(&req)
		if err != nil {
			response.FailWith(w, errorx.ErrAccessRecord, err.Error())
			return
		}
		response.Ok(w, resp)
	}
}
