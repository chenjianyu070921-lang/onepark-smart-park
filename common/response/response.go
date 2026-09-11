package response

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"onepark/common/errorx"
)

// Body 统一响应体 {code, msg, data}.
type Body struct {
	Code string      `json:"code"` // "0" 成功, "M1-E-1001" 失败
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// Ok 写入成功响应.
func Ok(w http.ResponseWriter, data interface{}) {
	httpx.OkJson(w, &Body{Code: errorx.SuccessCode, Msg: "ok", Data: data})
}

// Fail 写入失败响应.
func Fail(w http.ResponseWriter, err *errorx.CodeError) {
	httpx.WriteJson(w, err.HttpStatus(), &Body{Code: err.Code, Msg: err.Msg})
}

// FailWith 写入自定义 code/msg (走 500).
func FailWith(w http.ResponseWriter, code, msg string) {
	httpx.WriteJson(w, http.StatusInternalServerError, &Body{Code: code, Msg: msg})
}
