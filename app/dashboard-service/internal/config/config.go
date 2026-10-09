package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf

	// EnergyDataRpc M4 energy-data-service 的 gRPC 直连地址(接口54 GetDailyReport)
	EnergyDataRpc struct {
		Endpoint string // 例: 127.0.0.1:9061
	}
}
