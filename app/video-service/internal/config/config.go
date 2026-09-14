package config

import (
	"github.com/zeromicro/go-zero/core/stores/redis"
	"github.com/zeromicro/go-zero/rest"
)

// Config 服务配置 (M3 首个落地者, 见 KI-4).
type Config struct {
	rest.RestConf
	Redis redis.RedisConf
	MySQL MySQLConf
	Kafka KafkaConf
	ES    ESConf
	Nacos NacosConf
}

// MySQLConf GORM 数据源配置 (Day3 落地 model 层).
type MySQLConf struct {
	DataSource string `json:",default="`
	MaxIdle    int    `json:",default=10"`
	MaxOpen    int    `json:",default=100"`
}

// KafkaConf Kafka 生产/消费配置 (Day6 接入).
type KafkaConf struct {
	Brokers []string `json:",default=[]"`
	GroupID string   `json:",default=video-service-group"`
}

// ESConf Elasticsearch 配置 (Day13 接入).
type ESConf struct {
	Addresses []string `json:",default=[]"`
	Username  string   `json:",default="`
	Password  string   `json:",default=""`
}

// NacosConf 注册/配置中心 (可选).
type NacosConf struct {
	Address string `json:",default="`
}
