package types

// 登录/刷新/校验请求与响应.
type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResp struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	Expire       int64  `json:"expire"`
	UserId       int64  `json:"userId"`
	RoleIds      string `json:"roleIds"`
	TenantId     int64  `json:"tenantId"`
}

type RefreshReq struct {
	RefreshToken string `json:"refreshToken"`
}

type ValidateReq struct{}

type ValidateResp struct {
	UserId   int64  `json:"userId"`
	RoleIds  string `json:"roleIds"`
	TenantId int64  `json:"tenantId"`
}

// VerifyReq 携带待校验的 access 令牌(放在 body, 便于联调时主动探测令牌有效性).
type VerifyReq struct {
	Token string `json:"token"`
}

// VerifyResp 返回令牌是否合法及解析出的身份载荷.
// Valid=false 仅表示令牌无效/过期/类型不符, 不视为错误(HTTP 仍 200, 方便前端判断).
type VerifyResp struct {
	Valid     bool   `json:"valid"`
	UserId    int64  `json:"userId"`
	RoleIds   string `json:"roleIds"`
	TenantId  int64  `json:"tenantId"`
	ExpiresAt int64  `json:"expiresAt"`
}
