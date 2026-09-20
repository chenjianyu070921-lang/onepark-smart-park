// Package discovery 提供网关上游表的 Nacos 配置中心接入(仅改"上游地址来源", 不动转发逻辑).
// 设计原则: 可选、非致命、可回退.
//   - Nacos.Address 为空 -> 不启用, 网关使用静态 yaml 上游(本地联调行为不变).
//   - 启用但拉取失败 -> 回退静态 yaml 并持续重试, 不阻塞启动.
//   - 配置变更 -> 监听 OnChange 热更新路由表, 无需重启网关.
package discovery

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"onepark/gateway/internal/config"
	"onepark/gateway/internal/proxy"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// StartWatch 连接 Nacos 配置中心, 拉取网关上游表并在变更时热更新.
// 在独立 goroutine 中运行, 任何错误仅记录告警, 不影响网关主流程.
func StartWatch(c config.NacosConf, gw *proxy.Gateway) {
	host, port := splitHostPort(c.Address)
	sc := []constant.ServerConfig{*constant.NewServerConfig(host, port)}
	cc := constant.ClientConfig{
		NamespaceId: c.Namespace,
		Username:    c.Username,
		Password:    c.Password,
	}
	client, err := clients.NewConfigClient(vo.NacosClientParam{ServerConfigs: sc, ClientConfig: &cc})
	if err != nil {
		log.Printf("[nacos] gateway upstream watch disabled (init failed, fallback static): %v", err)
		return
	}

	param := vo.ConfigParam{DataId: c.DataId, Group: c.Group}
	content, err := client.GetConfig(param)
	if err != nil {
		log.Printf("[nacos] get upstream config failed (fallback static yaml): %v", err)
	} else if err := applyUpstreams(gw, content); err != nil {
		log.Printf("[nacos] parse upstream config failed (fallback static yaml): %v", err)
	}

	// 热更新: 配置中心变更时重新加载路由表.
	param.OnChange = func(_, _, _, data string) {
		if err := applyUpstreams(gw, data); err != nil {
			log.Printf("[nacos] upstream hot-reload failed (kept previous routes): %v", err)
			return
		}
		log.Printf("[nacos] upstream reloaded from %s/%s", c.Group, c.DataId)
	}
	if err := client.ListenConfig(param); err != nil {
		log.Printf("[nacos] listen upstream config failed: %v", err)
	}
}

// applyUpstreams 将 Nacos 配置内容(JSON 数组 [{prefix,target}])解析并原子替换网关路由表.
func applyUpstreams(gw *proxy.Gateway, content string) error {
	var ups []config.UpstreamConf
	if err := json.Unmarshal([]byte(content), &ups); err != nil {
		return err
	}
	return gw.Reload(ups)
}

// splitHostPort 解析 "host:port", 缺省端口回退 8848.
func splitHostPort(addr string) (string, uint64) {
	host := addr
	port := uint64(8848)
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
		if p, err := strconv.ParseUint(addr[i+1:], 10, 64); err == nil {
			port = p
		}
	}
	return host, port
}
