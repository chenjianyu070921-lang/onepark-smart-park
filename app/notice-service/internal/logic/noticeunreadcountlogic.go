package logic

import (
	"context"

	"onepark/app/notice-service/internal/model"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// NoticeUnreadCountLogic 站内信未读计数逻辑.
type NoticeUnreadCountLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewNoticeUnreadCountLogic(ctx context.Context, svcCtx *svc.ServiceContext) *NoticeUnreadCountLogic {
	return &NoticeUnreadCountLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// NoticeUnreadCount 返回当前登录用户的站内信未读数:
// notice_read 中 user_id=当前用户 且 read_at IS NULL 的送达记录条数.
// 口径: 仅统计"按人送达"的站内通知(工单事件等), 广播型公告不落 notice_read,
// 由前端按公告全量拉取, 不计入未读数.
func (l *NoticeUnreadCountLogic) NoticeUnreadCount() (resp *types.NoticeUnreadCountResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	uid := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	if uid <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少用户信息(x-user-id)")
	}

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	// P1 高频读优化: 先查 Redis 读穿缓存(60s TTL), 命中直接返回不落 DB.
	// 未命中/缓存不可用一律回落 DB COUNT, 缓存层不参与错误语义.
	if unread, ok := getUnreadCache(l.ctx, l.svcCtx.Redis, tenantID, uid); ok {
		return &types.NoticeUnreadCountResp{UnreadCount: unread}, nil
	}

	var unread int64
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.NoticeRead{}).
		Where("tenant_id=? AND user_id=? AND read_at IS NULL", tenantID, uid).
		Count(&unread).Error; e != nil {
		l.Errorf("count unread notices failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计未读通知失败")
	}

	// 回填缓存: 失败静默(仅记日志), 不影响本次返回.
	setUnreadCache(l.ctx, l.svcCtx.Redis, tenantID, uid, unread)

	return &types.NoticeUnreadCountResp{UnreadCount: unread}, nil
}
