package errorx

import "net/http"

// HttpStatus 将错误码级别映射到 HTTP 状态码.
func (e *CodeError) HttpStatus() int {
	if len(e.Code) < 4 {
		return http.StatusInternalServerError
	}
	// 形如 M1-E-1001, 第 3 位是级别
	level := string(e.Code[3])
	switch level {
	case LevelError:
		return http.StatusInternalServerError
	case LevelWarn:
		return http.StatusBadRequest
	case LevelInfo:
		return http.StatusOK
	default:
		return http.StatusInternalServerError
	}
}
