package handler

import (
	"errors"
	"net/http"

	"onepark/common/errorx"
	"onepark/common/response"
)

// writeErr 统一把 error 转成团队的 {code, msg} 响应
func writeErr(w http.ResponseWriter, err error) {
	var ce *errorx.CodeError
	if errors.As(err, &ce) {
		response.Fail(w, ce)
		return
	}
	response.FailWith(w, errorx.ErrInternal, err.Error())
}
