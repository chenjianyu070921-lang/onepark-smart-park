// Package jwt 提供 JWT 签发/校验的共享实现, 供网关与 auth-service 复用, 避免重复实现.
// 载荷字段与 common/ctxdata 对齐 (user_id/role_ids/tenant_id), 网关据此注入身份 Header.
package jwt

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// Claims 自定义 JWT 载荷, 字段与网关/业务侧 ctxdata 对齐 (user_id/role_ids/tenant_id).
type Claims struct {
	UserId   int64  `json:"user_id"`
	RoleIds  string `json:"role_ids"`
	TenantId int64  `json:"tenant_id"`
	Type     string `json:"type"` // access / refresh
	jwt.RegisteredClaims
}

// TokenType 区分访问令牌与刷新令牌.
const (
	TypeAccess  = "access"
	TypeRefresh = "refresh"
)

// Generate 签发 JWT. secret 必须来自环境变量, 调用方负责校验非空.
func Generate(secret string, userID int64, roleIDs string, tenantID int64, tokenType string, expireSeconds int64) (string, error) {
	now := time.Now()
	claims := Claims{
		UserId:   userID,
		RoleIds:  roleIDs,
		TenantId: tenantID,
		Type:     tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expireSeconds) * time.Second)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Parse 校验并解析 JWT, 返回 Claims. 签名/过期错误均返回 error.
func Parse(secret, tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
