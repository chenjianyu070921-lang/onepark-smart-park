package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"

	"onepark/common/ctxdata"

	"github.com/zeromicro/go-zero/core/logx"
)

// statusWriter 包装 ResponseWriter 以捕获响应状态码(供访问日志使用), 并透传 Hijacker.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hj.Hijack()
}

// AccessLog 访问日志 middleware: 记录 rid/method/path/status/cost/remote.
// 依赖 RequestIdMiddleware 注入的 x-request-id(从 context 读取).
func AccessLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := ctxdata.GetRequestId(r.Context())
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(sw, r)
		logx.Infof("[ACCESS] rid=%s method=%s path=%s status=%d cost=%s remote=%s",
			rid, r.Method, r.URL.Path, sw.status, time.Since(start), r.RemoteAddr)
	}
}
