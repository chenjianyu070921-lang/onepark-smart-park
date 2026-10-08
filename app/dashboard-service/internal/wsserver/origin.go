package wsserver

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"
)

// OriginChecker 判定 WebSocket 握手来源是否可信。
//
// 为什么必须收敛(而不是 `return true`): 浏览器的 WebSocket **不受同源策略限制** ——
// 任意站点都能用受害者浏览器发起 `new WebSocket("wss://大屏/ws/dashboard?token=...")`。
// 握手阶段浏览器会自动带上 Cookie/以及被脚本掌握的 token, 服务端若不看 Origin,
// 就等于允许"任何网站借用用户浏览器连我们的实时通道"(跨站 WebSocket 劫持)。
//
// 规则(自上而下, 命中即返回):
//  1. **没有 Origin** -> 允许。非浏览器客户端(压测工具、服务端进程、curl)本就不会带,
//     而 CSRF 的前提是"浏览器自动携带凭据", 这类客户端不构成该威胁;
//     带凭据的连接仍要过 ?token= 校验, 两道门是叠加关系而非二选一。
//  2. 白名单含 `*` -> 允许一切。仅供本机联调, 启动时已打 warn(见 logAllowOrigins)。
//  3. 与请求 Host **同源** -> 允许。前端与网关同域部署时的常见形态, 无需额外配置。
//  4. 命中白名单 -> 允许。按 `scheme://host[:port]` **精确比对**(大小写不敏感, 忽略末尾 '/' 与 path)。
//  5. 其余 -> **拒绝**, 并打出 Origin 与 Host —— 被拒时最需要的就是这一行日志。
func OriginChecker(allow []string) func(*http.Request) bool {
	normalized := make(map[string]struct{}, len(allow))
	wildcard := false
	for _, o := range allow {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			wildcard = true
			continue
		}
		normalized[normalizeOrigin(o)] = struct{}{}
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}

		if wildcard {
			logx.WithContext(r.Context()).Infof(
				"[ws] Origin 放行(白名单为 *): origin=%s host=%s —— 生产环境应改为具体域名", origin, r.Host)
			return true
		}

		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			logx.WithContext(r.Context()).Errorf("[ws] Origin 无法解析, 拒绝: origin=%q host=%s", origin, r.Host)
			return false
		}

		// 同源: 只比 host(含端口), 不比 scheme —— 反向代理下浏览器看到的可能是 https 而服务端是 http。
		if strings.EqualFold(u.Host, r.Host) {
			return true
		}

		if _, ok := normalized[normalizeOrigin(origin)]; ok {
			return true
		}

		logx.WithContext(r.Context()).Errorf(
			"[ws] Origin 不在白名单, 拒绝接入: origin=%s host=%s allow=%v —— 若前端用了新域名, 需同步 Ws.AllowOrigins",
			origin, r.Host, allow)
		return false
	}
}

// normalizeOrigin 归一化: 小写、去掉 path/query/末尾斜杠。
// 不归一化的话 `https://a.com/` 与 `https://a.com` 会被当成两个来源 ——
// 配置里多个斜杠就悄悄失效, 排查起来很费劲。
func normalizeOrigin(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		scheme := u.Scheme
		if scheme == "" {
			scheme = "http"
		}
		return scheme + "://" + u.Host
	}
	return strings.TrimSuffix(s, "/")
}

// logAllowOrigins 启动时把最终生效的来源策略打出来。
// 白名单为空最容易误解: 有人以为"没配 = 放开", 实际是"没配 = 只允许同源"。
func logAllowOrigins(allow []string) {
	if len(allow) == 0 {
		logx.Infof("[ws] 来源策略: 仅同源(未配置 Ws.AllowOrigins); 非浏览器客户端(无 Origin)不受限")
		return
	}
	logx.Infof("[ws] 来源策略: 白名单=%v (同源始终放行)", allow)
}
