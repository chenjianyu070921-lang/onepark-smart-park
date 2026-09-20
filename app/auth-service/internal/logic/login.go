package logic

import (
	"context"
	"strconv"
	"strings"

	"onepark/app/auth-service/internal/model"
	"onepark/app/auth-service/internal/svc"
	"onepark/app/auth-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/jwt"

	"github.com/zeromicro/go-zero/core/logx"
	"golang.org/x/crypto/bcrypt"
)

type LoginLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLoginLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LoginLogic {
	return &LoginLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// Login 校验用户名/密码(查 sys_db.sys_user + bcrypt), 签发 access + refresh 双令牌.
func (l *LoginLogic) Login(req *types.LoginReq) (resp *types.LoginResp, err error) {
	user, derr := model.FindUserByUsername(l.ctx, l.svcCtx.DB, req.Username)
	if derr != nil {
		// 用户名不存在或查询异常均归为凭证错误, 避免泄露账号是否存在.
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid username or password")
	}
	if user.Status != 1 {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "account disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		return nil, errorx.NewError(errorx.ErrUnauthorized, "invalid username or password")
	}

	roleIds, rerr := model.ListRoleIDs(l.ctx, l.svcCtx.DB, user.ID)
	if rerr != nil {
		l.Errorf("查询用户角色失败: %v", rerr)
		return nil, errorx.NewError(errorx.ErrInternal, "查询用户角色失败")
	}
	roleStr := joinRoleIDs(roleIds)

	access, gerr := jwt.Generate(l.svcCtx.JwtSecret, int64(user.ID), roleStr, user.TenantId, jwt.TypeAccess, l.svcCtx.JwtExpire)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}
	refresh, gerr := jwt.Generate(l.svcCtx.JwtSecret, int64(user.ID), roleStr, user.TenantId, jwt.TypeRefresh, l.svcCtx.JwtRefresh)
	if gerr != nil {
		return nil, errorx.NewError(errorx.ErrInternal, gerr.Error())
	}

	return &types.LoginResp{
		Token:        access,
		RefreshToken: refresh,
		Expire:       l.svcCtx.JwtExpire,
		UserId:       int64(user.ID),
		RoleIds:      roleStr,
		TenantId:     user.TenantId, // 回填用户真实所属园区(tenant_id 已由 M6 多租户试点迁移加入 sys_user 并回填)
	}, nil
}

// joinRoleIDs 将角色 ID 列表拼接为逗号分隔字符串, 与 JWT/网关约定一致.
func joinRoleIDs(ids []uint64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatUint(id, 10))
	}
	return strings.Join(parts, ",")
}
