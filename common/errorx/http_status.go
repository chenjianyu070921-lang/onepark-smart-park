package errorx

import "net/http"

// HttpStatus 将错误码映射为 HTTP 状态码.
// 约定: 成功码 "0" → 200; 已知语义码按语义精确映射(如未授权→401);
// 其余 E 级(业务/客户端错误, 如参数错误、资源不存在) → 400; W 级 → 400; I 级 → 200.
// 历史实现将未精确映射的 E 级一律归 500, 导致未登录/参数错误/不存在等客户端错误返回 500, 不符合 REST 语义.
func (e *CodeError) HttpStatus() int {
	switch e.Code {
	case SuccessCode:
		return http.StatusOK
	// 已知语义码精确映射
	case ErrBadRequest:
		return http.StatusBadRequest
	case ErrUnauthorized:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrNotFound:
		return http.StatusNotFound
	case ErrRateLimited:
		return http.StatusTooManyRequests
	case ErrBadGateway:
		return http.StatusBadGateway
	case ErrInternal, ErrDepConnect:
		return http.StatusInternalServerError
	}
	if len(e.Code) < 4 {
		return http.StatusInternalServerError
	}
	// 形如 M1-E-1001, 第 4 位是级别(索引 3)
	level := string(e.Code[3])
	switch level {
	case LevelError:
		// 未精确映射的 E 级业务错误归 400(客户端错误), 不再误用 500
		return http.StatusBadRequest
	case LevelWarn:
		return http.StatusBadRequest
	case LevelInfo:
		return http.StatusOK
	default:
		return http.StatusInternalServerError
	}
}
