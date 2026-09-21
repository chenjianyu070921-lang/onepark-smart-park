package logic

import (
	"context"
	"encoding/json"
	"time"

	"onepark/app/notice-service/internal/model"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// RecallNoticeLogic 撤回已发布公告逻辑: 仅已发布(2)可撤回 -> 置已撤回(3) + 审计字段 + 补偿事件.
type RecallNoticeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRecallNoticeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RecallNoticeLogic {
	return &RecallNoticeLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// RecallNotice 撤回公告.
// 入参: 路径 id + 可选撤回原因 req.Reason.
// 返回: 公告ID/撤回后状态(3已撤回).
// 约束: 仅已发布(2)可撤回; 草稿(1)未发布、已撤回(3)不可重复撤回.
func (l *RecallNoticeLogic) RecallNotice(id int64, req *types.RecallNoticeReq) (resp *types.RecallNoticeResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	var notice model.Notice
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("id=? AND tenant_id=?", id, tenantID).First(&notice).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrNoticeNotFound, "公告不存在")
		}
		l.Errorf("load notice failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载公告失败")
	}

	// 仅已发布可撤回: 草稿(1)未发布、已撤回(3)不可重复撤回.
	if notice.Status != model.NoticeStatusPublished {
		return nil, errorx.NewError(errorx.ErrNoticeNotPublished, "仅已发布(2)公告可撤回")
	}

	now := time.Now()
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.Notice{}).
		Where("id=? AND tenant_id=?", id, tenantID).
		Updates(map[string]interface{}{
			"status":        model.NoticeStatusWithdrawn,
			"recalled_at":   &now,
			"recall_reason": req.Reason,
			"updated_at":    now,
		}).Error; e != nil {
		l.Errorf("recall notice failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "撤回公告失败")
	}

	// 补偿事件: 重新投递 notice-event(status=3), 在线前端据状态下架公告横幅/红点.
	if l.svcCtx.Producer != nil {
		notice.Status = model.NoticeStatusWithdrawn
		notice.RecalledAt = &now
		notice.RecallReason = req.Reason
		notice.UpdatedAt = now
		if b, e := json.Marshal(notice); e == nil {
			if e := l.svcCtx.Producer.Publish(l.ctx, kafka.TopicNotice, []byte(notice.Title), b); e != nil {
				l.Errorf("publish recall notice event failed: %v", e)
			}
		}
	}

	return &types.RecallNoticeResp{Id: id, Status: model.NoticeStatusWithdrawn}, nil
}
