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

func PermissionCheckHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CheckPermissionReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := logic.NewPermissionCheckLogic(r.Context(), svcCtx).PermissionCheck(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
