package svc

import (
	"crypto/sha256"
	"encoding/hex"

	"onepark/app/auth-service/internal/config"
)

// UserInfo 内存用户视图(演示用, 生产应替换为用户中心/DB 查询).
type UserInfo struct {
	UserId   int64
	RoleIds  string
	TenantId int64
	PwdHash  string
}

// ServiceContext 注入 JWT 配置与用户表.
type ServiceContext struct {
	Config     config.Config
	JwtSecret  string
	JwtExpire  int64
	JwtRefresh int64
	Users      map[string]*UserInfo
}

// NewServiceContext 构建服务上下文, JWT_SECRET 为空直接 panic(禁止硬编码密钥).
func NewServiceContext(c config.Config) *ServiceContext {
	if c.JwtSecret == "" {
		panic("auth-service: JWT_SECRET is required, set environment variable JWT_SECRET")
	}
	expire := c.JwtExpire
	if expire <= 0 {
		expire = 7200
	}
	refresh := c.JwtRefresh
	if refresh <= 0 {
		refresh = 86400
	}

	users := make(map[string]*UserInfo, len(c.Users))
	for i, u := range c.Users {
		hash := u.PasswordHash
		if hash == "" && u.Password != "" {
			h := sha256.Sum256([]byte(u.Password))
			hash = hex.EncodeToString(h[:])
		}
		users[u.Username] = &UserInfo{
			UserId:   int64(i + 1),
			RoleIds:  u.RoleIds,
			TenantId: u.TenantId,
			PwdHash:  hash,
		}
	}

	return &ServiceContext{
		Config:     c,
		JwtSecret:  c.JwtSecret,
		JwtExpire:  expire,
		JwtRefresh: refresh,
		Users:      users,
	}
}
