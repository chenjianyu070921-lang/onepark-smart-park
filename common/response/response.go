package response

import (
	"context"
	"errors"
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

// Init 注册 go-zero 全局响应处理器, 将 httpx.OkJson / httpx.Error 统一包装为 {code,msg,data}.
// 各服务 main 启动时调用一次即可, 无需逐 handler 改造.
// 适配 go-zero v1.10.3 的 SetOkHandler/SetErrorHandler 新签名: func(ctx, resp any) any / func(ctx, err error) (int, any).
func Init() {
	httpx.SetOkHandler(func(ctx context.Context, resp any) any {
		return &Body{Code: errorx.SuccessCode, Msg: "ok", Data: resp}
	})
	httpx.SetErrorHandler(func(err error) (int, any) {
		var ce *errorx.CodeError
		if errors.As(err, &ce) {
			return ce.HttpStatus(), &Body{Code: ce.Code, Msg: ce.Msg}
		}
		// 非 CodeError(参数解析/系统错误)归 400, 避免裸 500.
		return http.StatusBadRequest, &Body{Code: errorx.ErrBadRequest, Msg: err.Error()}
	})
}
