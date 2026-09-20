package logic

import (
	"context"
	"time"

	"onepark/app/notice-service/internal/model"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// MarkNoticeReadLogic 公告已读回填逻辑.
type MarkNoticeReadLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMarkNoticeReadLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MarkNoticeReadLogic {
	return &MarkNoticeReadLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// MarkNoticeRead 当前登录用户对指定公告回填已读时间(read_at=now).
// 仅回填"本人未读"的送达记录(read_at IS NULL), 已读重复调用 updated=0(幂等);
// 广播型公告无送达记录, updated=0 由前端按列表已读自行处理.
func (l *MarkNoticeReadLogic) MarkNoticeRead(req *types.MarkNoticeReadReq) (resp *types.MarkNoticeReadResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	uid := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	if uid <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少用户信息(x-user-id)")
	}
	if req.NoticeId <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "notice_id 非法")
	}

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	now := time.Now()
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.NoticeRead{}).
		Where("tenant_id=? AND notice_id=? AND user_id=? AND read_at IS NULL", tenantID, req.NoticeId, uid).
		Update("read_at", now)
	if res.Error != nil {
		l.Errorf("mark notice read failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrM2Internal, "回填已读失败")
	}

	return &types.MarkNoticeReadResp{Updated: res.RowsAffected}, nil
}
