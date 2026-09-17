package config

import "github.com/zeromicro/go-zero/rest"

// Config 鉴权服务配置.
type Config struct {
	rest.RestConf
	JwtSecret  string `json:",optional"` // JWT 签名密钥, 必须来自环境变量 JWT_SECRET, 禁止硬编码
	JwtExpire  int64  `json:",optional"` // access token 有效期(秒), 默认 7200
	JwtRefresh int64  `json:",optional"` // refresh token 有效期(秒), 默认 86400
	// MySQL 指向用户中心库(sys_db), 身份校验改为查询 sys_user + sys_user_role.
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
