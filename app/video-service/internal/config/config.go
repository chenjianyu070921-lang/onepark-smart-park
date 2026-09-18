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
	Redis  redis.RedisConf `json:",optional"`
	MySQL  gormx.MySQLConf `json:",optional"` // video_db 连接配置(摄像头元数据)
	Stream StreamConf      `json:",optional"` // 流地址下发配置(#51)
	Kafka  KafkaConf       `json:",optional"`
	ES     ESConf          `json:",optional"`
	Nacos  NacosConf       `json:",optional"`
}

// StreamConf 视频流下发配置(docs/m3/04 #51).
// M3 只管理与下发流地址, 不实现转码/推流; flv 地址由独立流媒体服务提供,
// 未配置 FlvBaseURL 时只返回 RTSP 地址(不编造不可用的地址).
type StreamConf struct {
	FlvBaseURL     string `json:",optional"`     // FLV 拉流基地址, 形如 http://media/live
	ExpiresSeconds int    `json:",default=3600"` // 流地址有效期(秒)
}

// KafkaConf Kafka 生产/消费配置 (Day6 接入).
type KafkaConf struct {
	Brokers []string `json:",default=[]"`
	GroupID string   `json:",default=video-service-group"`
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
