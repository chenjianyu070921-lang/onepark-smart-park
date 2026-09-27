package ws

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"onepark/common/jwt"

	"github.com/gorilla/websocket"
	"github.com/zeromicro/go-zero/core/logx"
)

// upgrader 将 HTTP 连接升级为 WebSocket(未启用鉴权时的默认实例, 保持既有行为).
var upgrader = newUpgrader(nil)

// newUpgrader 按鉴权配置构造 upgrader.
//
// CheckOrigin 默认恒真, 只有在配置了来源白名单后才收紧 —— 与鉴权同样的渐进策略:
// 本地联调与多域名前端接入时不应被拦住, 但生产一旦配了白名单就严格比对。
func newUpgrader(auth *Authenticator) websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if auth == nil || len(auth.origins) == 0 {
				return true
			}
			return auth.allowOrigin(r)
		},
	}
}

// Authenticator WS 握手鉴权器(2026-09-24 落地, 依赖 M6 common/jwt).
//
// 背景: 此前租户靠 query ?tenant_id= 传入且 CheckOrigin 恒真 —— 任意来源、任意租户号
// 都能连上并收到该园区的全部告警。这是审计列的 P0-2, 长期阻塞在"等 M6 JWT 方案"。
type Authenticator struct {
	secret  string
	origins map[string]struct{}
}

// NewAuthenticator 构造鉴权器; secret 为空表示**未启用**(调用方不应构造本对象).
func NewAuthenticator(secret string, origins []string) *Authenticator {
	a := &Authenticator{secret: secret, origins: make(map[string]struct{}, len(origins))}
	for _, o := range origins {
		if o = strings.TrimSpace(o); o != "" {
			a.origins[strings.ToLower(o)] = struct{}{}
		}
	}
	return a
}

// tenantFromRequest 校验握手请求并返回租户.
//
// token 的三个来源按优先级: query ?token= → Authorization: Bearer → Sec-WebSocket-Protocol。
// **浏览器 WS 握手无法自定义 header**, 因此 query 与子协议两个通道必须都支持:
// 只认 header 会让浏览器前端根本连不上, 只认 query 又会逼前端把令牌写进 URL(易泄漏到日志)。
//
// 租户一律**取自令牌**, 不再信任 query 的 tenant_id:
// 若调用方同时传了 tenant_id 且与令牌中的租户不一致, 直接拒绝 —— 这类请求要么是配置错误,
// 要么是在探测"换个租户号能不能看到别人的告警"。
func (a *Authenticator) tenantFromRequest(r *http.Request) (int64, error) {
	token := extractToken(r)
	if token == "" {
		return 0, errors.New("缺少令牌(token)")
	}
	claims, err := jwt.Parse(a.secret, token)
	if err != nil {
		return 0, errors.New("令牌校验失败")
	}
	if claims.Type != jwt.TypeAccess {
		// 刷新令牌只用于换发, 不应具备业务连接权限(缩小令牌泄露后的影响面).
		return 0, errors.New("令牌类型不允许用于业务连接")
	}
	if claims.TenantId <= 0 {
		return 0, errors.New("令牌缺少有效租户")
	}
	if raw := r.URL.Query().Get("tenant_id"); raw != "" {
		if q, err := strconv.ParseInt(raw, 10, 64); err == nil && q != claims.TenantId {
			return 0, errors.New("tenant_id 与令牌租户不一致")
		}
	}
	return claims.TenantId, nil
}

// allowOrigin 严格比对 Origin 头与白名单(大小写不敏感, 已归一).
func (a *Authenticator) allowOrigin(r *http.Request) bool {
	origin := strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))
	if origin == "" {
		// 非浏览器客户端(服务端/CLI)可能不带 Origin; 白名单已启用时按"不允许"处理,
		// 否则放开等于白名单形同虚设。
		return false
	}
	_, ok := a.origins[origin]
	return ok
}

// extractToken 从三个通道提取令牌.
func extractToken(r *http.Request) string {
	if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
		return t
	}
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			return strings.TrimSpace(h[len("bearer "):])
		}
		return h
	}
	// 子协议通道: 浏览器可用 new WebSocket(url, ["bearer", token]) 携带.
	// 用 Values 而不是直接索引 Header map: Go 会把键规范化为 Sec-Websocket-Protocol,
	// 直接按字面量索引取不到(实测表现为"子协议通道恒 401")。
	for _, p := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(p, ",") {
			if t := strings.TrimSpace(part); t != "" && !strings.EqualFold(t, "bearer") {
				return t
			}
		}
	}
	return ""
}

// Handler 返回 /ws/alarm 的 HTTP handler(docs/m3/09 §3).
// go-zero 的 Use 中间件不作用于 AddRoutes 注册的路由, 故租户解析在此自行完成.
//
// 鉴权由 Hub.Auth 决定: 非 nil(即配置了 WS.AuthSecret)时**必须**携带有效令牌,
// 租户取自令牌; 为 nil 时退回 query ?tenant_id=(仅内网/网关后部署的既有行为).
func (h *Hub) Handler() http.HandlerFunc {
	up := newUpgrader(h.Auth)
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := h.resolveTenant(w, r)
		if !ok {
			return
		}

		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			logx.WithContext(r.Context()).Errorf("ws upgrade failed: %v", err)
			return
		}
		h.Register(newClient(h, c, tenantID))
	}
}

// resolveTenant 解析握手请求的租户, 失败时已写入响应.
func (h *Hub) resolveTenant(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if h.Auth != nil {
		tenantID, err := h.Auth.tenantFromRequest(r)
		if err != nil {
			// 401 与 400 的区分: 缺令牌/令牌无效是**身份问题**(客户端应重新登录),
			// 参数不一致才是请求问题。混用会让前端无法判断该跳转登录还是改参数。
			code := http.StatusUnauthorized
			if err.Error() == "tenant_id 与令牌租户不一致" {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return 0, false
		}
		return tenantID, true
	}
	tenantID, ok := parseTenant(r)
	if !ok {
		http.Error(w, "invalid tenant_id", http.StatusBadRequest)
		return 0, false
	}
	return tenantID, true
}

// parseTenant 从 query 解析园区ID; 缺失或非正数视为非法.
// 仅在未启用鉴权(Auth == nil)时使用, 保留给内网/网关后部署的场景.
func parseTenant(r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("tenant_id")
	if raw == "" {
		return 0, false
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		return 0, false
	}
	return tenantID, true
}
