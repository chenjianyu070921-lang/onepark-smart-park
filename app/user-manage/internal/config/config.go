package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf
	MySQL struct {
		DataSource   string
		MaxOpenConns int `json:",default=20"`
		MaxIdleConns int `json:",default=10"`
	}
}
