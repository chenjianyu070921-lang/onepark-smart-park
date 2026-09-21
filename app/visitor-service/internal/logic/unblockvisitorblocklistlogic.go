package logic

import (
	"context"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// UnblockVisitorBlocklistLogic 解除访客黑名单逻辑: 将已生效记录置为解除(2), 恢复通行.
type UnblockVisitorBlocklistLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUnblockVisitorBlocklistLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UnblockVisitorBlocklistLogic {
	return &UnblockVisitorBlocklistLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// UnblockVisitorBlocklist 解除指定黑名单(路径 id).
// 返回: 记录ID/解除后状态(2).
func (l *UnblockVisitorBlocklistLogic) UnblockVisitorBlocklist(id int64) (resp *types.UnblockVisitorBlocklistResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	now := time.Now()
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorBlocklist{}).
		Where("id=? AND tenant_id=? AND status=?", id, tenantID, model.BlocklistStatusActive).
		Updates(map[string]interface{}{"status": model.BlocklistStatusRemoved, "updated_at": now})
	if res.Error != nil {
		l.Errorf("unblock visitor blocklist failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrM2Internal, "解除黑名单失败")
	}
	if res.RowsAffected == 0 {
		return nil, errorx.NewError(errorx.ErrM2Internal, "黑名单记录不存在或已解除")
	}
	return &types.UnblockVisitorBlocklistResp{Id: id, Status: model.BlocklistStatusRemoved}, nil
}
