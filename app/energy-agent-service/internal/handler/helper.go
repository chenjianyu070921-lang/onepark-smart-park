package handler

import (
	"errors"
	"net/http"

	"onepark/common/errorx"
	"onepark/common/response"
)

// writeErr 统一把 error 转成团队的 {code, msg} 响应。
// 业务错误用它自己的错误码(如 M4-E-1011), 未知错误统一按内部错误处理,
// 免得把数据库报错原文直接甩给前端。
func writeErr(w http.ResponseWriter, err error) {
	var ce *errorx.CodeError
	if errors.As(err, &ce) {
		response.Fail(w, ce)
		return
	}
	response.FailWith(w, errorx.ErrInternal, err.Error())
}
