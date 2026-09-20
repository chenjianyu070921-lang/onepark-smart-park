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

// fail 统一错误响应包装: CodeError 透传, 其余归内部错误.
func fail(w http.ResponseWriter, err error) {
	if ce, ok := err.(*errorx.CodeError); ok {
		response.Fail(w, ce)
	} else {
		response.Fail(w, errorx.NewError(errorx.ErrInternal, err.Error()))
	}
}

func UserCreateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateUserReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := logic.NewUserCreateLogic(r.Context(), svcCtx).UserCreate(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

func UserUpdateHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateUserReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		if err := logic.NewUserUpdateLogic(r.Context(), svcCtx).UserUpdate(&req); err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, nil)
	}
}

func UserDeleteHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UserDeleteReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		if err := logic.NewUserDeleteLogic(r.Context(), svcCtx).UserDelete(&req); err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, nil)
	}
}

func UserDetailHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UserDetailReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := logic.NewUserDetailLogic(r.Context(), svcCtx).UserDetail(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}

func UserListHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UserListReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, err.Error()))
			return
		}
		resp, err := logic.NewUserListLogic(r.Context(), svcCtx).UserList(&req)
		if err != nil {
			fail(w, err)
			return
		}
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
