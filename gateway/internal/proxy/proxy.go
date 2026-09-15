// Package proxy 实现基于路径前缀的反向代理, 作为 OnePark 对外统一入口(网关).
// 职责: 按最长前缀匹配转发请求到上游服务; 注入 x-request-id 与 RBAC 上下文
// (x-tenant-id/x-user-id/x-role-ids); 统一 CORS; 上游不可用时返回 502 统一响应体.
package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"onepark/common/ctxdata"
	"onepark/gateway/internal/config"
)

// route 单条前缀路由.
type route struct {
	prefix string
	proxy  *httputil.ReverseProxy
}

// Gateway 对外网关(实现 http.Handler).
type Gateway struct {
	routes          []route // 按 prefix 长度降序, 保证最长前缀优先
	defaultTenantID int64
	defaultUserId   int64
}

// NewGateway 依据配置构建网关及其上游反向代理.
func NewGateway(c config.Config) (*Gateway, error) {
	g := &Gateway{
		defaultTenantID: c.DefaultTenantId,
		defaultUserId:   c.DefaultUserId,
	}
	for _, up := range c.Upstreams {
		target, err := url.Parse(up.Target)
		if err != nil {
			return nil, err
		}
		rp := httputil.NewSingleHostReverseProxy(target)
		prefix := up.Prefix
		rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			writeJSONError(w, http.StatusBadGateway, "M6-E-0006", "upstream unavailable ["+prefix+"]: "+e.Error())
		}
		g.routes = append(g.routes, route{prefix: prefix, proxy: rp})
	}
	// 最长前缀优先匹配.
	sort.Slice(g.routes, func(i, j int) bool {
		return len(g.routes[i].prefix) > len(g.routes[j].prefix)
	})
	return g, nil
}

// ServeHTTP 处理全部入站请求: CORS -> 注入上下文头 -> 最长前缀匹配转发.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 开发环境 CORS 全放通.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, X-Request-Id, X-Tenant-Id, X-User-Id, X-Role-Ids")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 注入 RequestId(透传或生成) 与 RBAC 上下文(缺省用配置默认值, 便于无鉴权联调).
	rid := r.Header.Get(ctxdata.CtxRequestId)
	if rid == "" {
		rid = newRequestId()
		r.Header.Set(ctxdata.CtxRequestId, rid)
	}
	w.Header().Set(ctxdata.CtxRequestId, rid)
	if r.Header.Get(ctxdata.CtxTenantId) == "" {
		r.Header.Set(ctxdata.CtxTenantId, strconv.FormatInt(g.defaultTenantID, 10))
	}
	if r.Header.Get(ctxdata.CtxUserId) == "" {
		r.Header.Set(ctxdata.CtxUserId, strconv.FormatInt(g.defaultUserId, 10))
	}

	// 最长前缀匹配转发.
	for _, rt := range g.routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			rt.proxy.ServeHTTP(w, r)
			return
		}
	}

	writeJSONError(w, http.StatusNotFound, "M6-E-0004", "no upstream route for path: "+r.URL.Path)
}

// writeJSONError 以统一响应体 {code,msg,data} 输出网关层错误.
func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": code,
		"msg":  msg,
		"data": nil,
	})
}

// newRequestId 生成简易 RequestId(网关层兜底).
func newRequestId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "rid-" + hex.EncodeToString(b)
}
