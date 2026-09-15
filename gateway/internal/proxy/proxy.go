package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/common/errorx"
	"onepark/common/response"
	"onepark/gateway/internal/config"
)

// svcPrefix 服务名 -> 网关 URL 前缀, 用于路径匹配转发.
// 约定: 各业务服务对外暴露 /api/<svc> 路由, 网关按此前缀转发.
var svcPrefix = map[string]string{
	"device":    "/api/devices",
	"workorder": "/api/workorders",
	"visitor":   "/api/visitors",
	"parking":   "/api/parking",
	"notice":    "/api/notices",
	"alarm":     "/api/alarms",
	"access":    "/api/access",
	"video":     "/api/video",
	"energy":    "/api/energy",
	"billing":   "/api/billing",
	"dashboard": "/api/dashboard",
	"leasing":   "/api/leasing",
	"dispatch":  "/api/dispatch",
}

type route struct {
	prefix string
	target *url.URL
}

// NewHandler 构建统一入口反向代理: 按 URL 前缀转发到对应业务服务 upstream.
// 路径原样透传 (保留 /api/<svc>/...), 业务服务自行处理路由.
func NewHandler(upstreams []config.Upstream) http.HandlerFunc {
	var routes []route
	for _, u := range upstreams {
		prefix, ok := svcPrefix[u.Name]
		if !ok {
			logx.Errorf("gateway: unknown upstream name %q, skipped", u.Name)
			continue
		}
		target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", u.Port))
		if err != nil {
			logx.Errorf("gateway: invalid upstream %q: %v", u.Name, err)
			continue
		}
		routes = append(routes, route{prefix: prefix, target: target})
	}
	// 最长前缀优先, 避免短前缀误吞更长前缀路径.
	sort.SliceStable(routes, func(i, j int) bool {
		return len(routes[i].prefix) > len(routes[j].prefix)
	})

	return func(w http.ResponseWriter, r *http.Request) {
		for _, rt := range routes {
			if strings.HasPrefix(r.URL.Path, rt.prefix) {
				proxy := &httputil.ReverseProxy{
					Director: func(req *http.Request) {
						req.URL.Scheme = rt.target.Scheme
						req.URL.Host = rt.target.Host
						req.Host = rt.target.Host
					},
					ErrorHandler: func(rw http.ResponseWriter, req *http.Request, e error) {
						logx.Errorf("gateway: proxy %s -> %s failed: %v", rt.prefix, rt.target.Host, e)
						response.Fail(rw, errorx.NewError(errorx.ErrBadGateway, "上游服务不可用: "+e.Error()))
					},
				}
				proxy.ServeHTTP(w, r)
				return
			}
		}
		// 未匹配任何服务前缀: 统一返回 errorx 风格 404, 与下游业务响应格式一致.
		response.Fail(w, errorx.NewError(errorx.ErrNotFound, "未匹配的服务路由"))
	}
}
