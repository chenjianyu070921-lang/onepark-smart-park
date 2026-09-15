package handler

import (
	"net/http"

	"onepark/app/user-manage/internal/logic"
	"onepark/app/user-manage/internal/svc"
	"onepark/app/user-manage/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func RoleCreateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateRoleReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := logic.NewRoleCreateLogic(r.Context(), svcCtx).RoleCreate(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

func RoleAssignHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AssignRoleReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		if err := logic.NewRoleAssignLogic(r.Context(), svcCtx).RoleAssign(&req); err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, nil)
	}
}
