package config

import (
	"onepark/common/gormx"

	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
)

// Config 定义 video-service 的运行配置.
// Day2 各中间件均为可选: 未接入前 yaml 不配置对应段落也能启动(见 P0-6 三服务同时启动验收).
type Config struct {
	rest.RestConf
	Redis     redis.RedisConf `json:",optional"`
	MySQL     gormx.MySQLConf `json:",optional"` // video_db 连接配置(摄像头元数据)
	Stream    StreamConf      `json:",optional"` // 流地址下发配置(#51)
	Kafka     KafkaConf       `json:",optional"` // 消费 M1 设备遥测(摄像头心跳)
	Heartbeat HeartbeatConf   `json:",optional"` // 心跳在线状态判定
	Record    RecordConf      `json:",optional"` // 录像计划与回放下发(#52/#53)
	ES        ESConf          `json:",optional"`
	Nacos     NacosConf       `json:",optional"`
}

// defaultCameraDeviceType M1 遥测中摄像头设备的默认 device_type.
// 与 app/alarm-service 里门禁的 access_control 同一套取值口径; 未配置白名单时用它兜底.
const defaultCameraDeviceType = "camera"

// StreamConf 视频流下发配置(docs/m3/04 #51).
// M3 只管理与下发流地址, 不实现转码/推流; flv 地址由独立流媒体服务提供,
// 未配置 FlvBaseURL 时只返回 RTSP 地址(不编造不可用的地址).
type StreamConf struct {
	FlvBaseURL     string `json:",optional"`     // FLV 拉流基地址, 形如 http://media/live
	ExpiresSeconds int    `json:",default=3600"` // 流地址有效期(秒)
	// SignSecret 流地址签名密钥(docs/m3/01 P1-6 / docs/m3/11 §时效性签名).
	//
	// 为空时**不签名**: 响应 sign 字段为空串, FLV 地址不追加 expires/sign 参数,
	// 行为与签名能力上线前完全一致(向后兼容, 本地联调无需配置密钥).
	// 一旦配置, 所有下发地址都带 HMAC-SHA256 签名, 校验逻辑见 logic.VerifyStreamSign。
	SignSecret string `json:",optional"`
}

// RecordConf 录像计划与回放下发配置.
//
// 签名密钥刻意复用 Stream.SignSecret: 拉流与回放是同一套"媒体访问授权",
// 拆成两个密钥只会增加"某个环境漏配一个"的概率, 而收益为零.
type RecordConf struct {
	// PlaybackBaseURL 回放地址基地址, 形如 http://media/record;
	// 为空时回放接口仍返回时间窗口, 但 playback_url 为空串(不编造不可用的地址).
	PlaybackBaseURL string `json:",optional"`
	// ExpiresSeconds 回放地址有效期(秒); <=0 时用 Stream.ExpiresSeconds, 再兜底 3600.
	ExpiresSeconds int `json:",default=3600"`
	// DefaultRetentionDays 新建计划未指定保留天数时的默认值; <=0 兜底为 7.
	DefaultRetentionDays int `json:",default=7"`
	// MaxRangeHours 单次回放查询允许的最大时间跨度(小时); <=0 兜底 24.
	// 必须限制: 一次拉几十天的窗口会让"按天遍历 ComputeAvailableWindows"退化成大量无谓计算,
	// 也让前端一次拿到成千上万段分段, 两端都不堪重负.
	MaxRangeHours int `json:",default=24"`
}

// KafkaConf Kafka 消费配置(摄像头心跳).
// Brokers 用 string 而非 []string: 与 alarm/parking 一致, 且 yaml 里就是一个 ${KAFKA_BROKERS}
// 环境变量(逗号分隔), 用切片会让"配了但没注入"变成 len=1 的空串元素而非空配置.
type KafkaConf struct {
	Brokers string `json:",optional"`
	GroupID string `json:",default=video-service"`
}

// HeartbeatConf 摄像头在线状态判定配置.
// 地址簿的 status 列不能靠人工维护: 摄像头掉线上线是常态, 人工改既滞后也不可靠.
type HeartbeatConf struct {
	// DeviceTypes 参与心跳判定的设备类型白名单. M1 的 device_type 取值尚未与 M3 逐项对齐,
	// 做成可配置而非硬编码, 避免上游换个词就让整个在线状态失效.
	DeviceTypes []string `json:",default=[camera]"`
	// OfflineAfterSeconds 超过该时长未收到心跳即判定离线(建议 >= 上报周期的 3 倍,
	// 否则偶发丢包会把正常摄像头误判离线).
	OfflineAfterSeconds int `json:",default=180"`
	// ScanIntervalSeconds 离线扫描周期.
	ScanIntervalSeconds int `json:",default=60"`
}

// DeviceTypeAllowed 判定该设备类型是否参与心跳. 未配置白名单时按默认 camera 处理.
func (c HeartbeatConf) DeviceTypeAllowed(deviceType string) bool {
	types := c.DeviceTypes
	if len(types) == 0 {
		types = []string{defaultCameraDeviceType}
	}
	for _, t := range types {
		if t == deviceType {
			return true
		}
	}
	return false
}

// ESConf Elasticsearch 配置 (Day13 接入).
type ESConf struct {
	Addresses []string `json:",default=[]"`
	Username  string   `json:",optional"`
	Password  string   `json:",optional"`
}

// NacosConf 注册/配置中心 (可选).
type NacosConf struct {
	Address string `json:",default="`
}
