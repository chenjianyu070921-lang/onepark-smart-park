package config

import "github.com/zeromicro/go-zero/zrpc"

type Config struct {
	zrpc.RpcServerConf
	MySQL struct {
		// DataSource 支持 ${MYSQL_DSN} 环境变量占位, 由 ServiceContext 做 os.ExpandEnv
		DataSource   string
		MaxOpenConns int `json:",default=20"`
		MaxIdleConns int `json:",default=10"`
	}
}
