package config

import (
	"testing"

	"github.com/zeromicro/go-zero/core/conf"
)

// TestLoadAlarmConfig 真实加载 etc/alarm-api.yaml, 验证 YAML 可被 go-zero 解析.
//
// 重点锁定 WS.BroadcastChannel: 它留空时 Hub 会**静默**退化为单实例内存广播 ——
// 单副本跑起来一切正常, 多副本上线才会暴露"大屏收不到其他实例推的告警",
// 是很难定位的问题, 因此在配置层加一道防线(空值不报错, 但必须有人发现)。
// 注: yaml 中的 ${VAR} 占位符在本测试中不解环境变量(不传 conf.UseEnv()),
// 解析结果保留字面量, 不影响"字段是否被配置"的断言。
func TestLoadAlarmConfig(t *testing.T) {
	var c Config
	conf.MustLoad("../../etc/alarm-api.yaml", &c)

	if c.WS.BroadcastChannel == "" {
		t.Error("etc/alarm-api.yaml 未配置 WS.BroadcastChannel: 多副本部署时 WebSocket 广播将退化为单实例")
	}
	// 其余关键字段也一并锁定: 端口冲突会直接导致服务起不来(P0-6).
	if c.Port != 8009 {
		t.Errorf("alarm-api 端口应为 8009, 实际 %d", c.Port)
	}
	if c.Kafka.GroupID == "" {
		t.Error("Kafka.GroupID 未配置: 消费组缺失会导致多实例重复消费")
	}
}
