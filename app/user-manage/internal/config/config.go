package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf
	MySQL struct {
		DataSource   string
		MaxOpenConns int `json:",default=20"`
		MaxIdleConns int `json:",default=10"`
	}

	// Grpc 双模 gRPC 监听地址(可选). 为空则仅暴露 HTTP(网关行为不变), 不启 gRPC server.
	Grpc struct {
		ListenOn string `json:",optional"`
	} `json:",optional"`
}
