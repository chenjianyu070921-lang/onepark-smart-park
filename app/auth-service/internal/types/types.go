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
