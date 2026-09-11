package errorx

import "fmt"

// CodeError 统一错误类型, 携带错误码与消息.
type CodeError struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

func NewError(code, msg string) *CodeError {
	return &CodeError{Code: code, Msg: msg}
}

func (e *CodeError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}
