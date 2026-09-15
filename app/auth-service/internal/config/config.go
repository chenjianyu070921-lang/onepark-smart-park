package config

import "github.com/zeromicro/go-zero/rest"

// Config 鉴权服务配置.
type Config struct {
	rest.RestConf
	JwtSecret  string       `json:",optional"` // JWT 签名密钥, 必须来自环境变量 JWT_SECRET, 禁止硬编码
	JwtExpire  int64        `json:",optional"` // access token 有效期(秒), 默认 7200
	JwtRefresh int64        `json:",optional"` // refresh token 有效期(秒), 默认 86400
	Users      []UserConfig `json:",optional"` // 初始用户(演示用, 生产应接入用户中心/DB)
}

// UserConfig 初始用户配置. 密码优先使用预计算的 PasswordHash(sha256 hex),
// 未配置时回退用 Password 明文(启动时转 sha256), 仅供本地演示.
type UserConfig struct {
	Username     string `json:",optional"`
	Password     string `json:",optional"`
	PasswordHash string `json:",optional"`
	RoleIds      string `json:",optional"`
	TenantId     int64  `json:",optional"`
}
