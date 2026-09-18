package ws

import (
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/zeromicro/go-zero/core/logx"
)

// upgrader 将 HTTP 连接升级为 WebSocket.
// Origin 校验放到网关侧(生产由网关统一做跨域与来源控制); 此处放开是为了
// 本地联调与多域名前端接入, 生产环境应收敛为白名单.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Handler 返回 /ws/alarm 的 HTTP handler(docs/m3/09 §3).
// go-zero 的 Use 中间件不作用于 AddRoutes 注册的路由, 故租户解析在此自行完成:
// 浏览器 WS 握手无法自定义 header, 租户通过 query ?tenant_id= 传递.
//
// ⚠️ 鉴权待补(R2): M6 JWT 校验方式确认后, 应在此校验 ?token= 并从 token 解析租户,
// 届时不再信任 query 传入的 tenant_id(当前仅适用于内网/网关后部署).
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := parseTenant(r)
		if !ok {
			http.Error(w, "invalid tenant_id", http.StatusBadRequest)
			return
		}

		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logx.WithContext(r.Context()).Errorf("ws upgrade failed: %v", err)
			return
		}
		h.Register(newClient(h, c, tenantID))
	}
}

// parseTenant 从 query 解析园区ID; 缺失或非正数视为非法.
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
