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
)

// CreateNoticeLogic 发布公告/通知逻辑.
// 发布时间 publish_at=0 表示立即发布(置已发布), 否则置草稿(待定时任务发布).
type CreateNoticeLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateNoticeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateNoticeLogic {
	return &CreateNoticeLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CreateNotice 处理发布公告请求.
// 入参: 标题/正文/类型/是否置顶/定时发布时间(可选).
// 返回: 公告ID/标题/类型/状态/是否置顶/发布时间.
func (l *CreateNoticeLogic) CreateNotice(req *types.CreateNoticeReq) (resp *types.NoticeResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	publisherID := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	now := time.Now()
	// 立即发布: publish_at=0 置已发布; 定时发布: 置草稿(由调度任务后续发布).
	status := model.NoticeStatusDraft
	var publishAt *time.Time
	if req.PublishAt == 0 {
		status = model.NoticeStatusPublished
		publishAt = &now
	} else {
		publishAt = unixPtrNotice(req.PublishAt)
	}

	notice := &model.Notice{
		Title:       req.Title,
		Content:     req.Content,
		Type:        req.Type,
		PublisherID: publisherID,
		Top:         boolToInt8(req.Top),
		Status:      status,
		PublishAt:   publishAt,
	}
	notice.TenantID = tenantID
	notice.CreatedAt = now
	notice.UpdatedAt = now

	if e := l.svcCtx.DB.WithContext(l.ctx).Create(notice).Error; e != nil {
		l.Errorf("create notice failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "发布公告失败")
	}

	// 已发布则发布事件, 供通知类服务订阅推送.
	if status == model.NoticeStatusPublished && l.svcCtx.Producer != nil {
		b, _ := json.Marshal(notice)
		if e := l.svcCtx.Producer.Publish(l.ctx, kafka.TopicNotice, []byte(notice.Title), b); e != nil {
			l.Errorf("publish notice event failed: %v", e)
		}
	}

	return &types.NoticeResp{
		Id:        notice.ID,
		Title:     notice.Title,
		Type:      notice.Type,
		Status:    notice.Status,
		Top:       req.Top,
		PublishAt: timeOrUnixNotice(notice.PublishAt),
	}, nil
}

// boolToInt8 bool 转 int8(0/1).
func boolToInt8(b bool) int8 {
	if b {
		return 1
	}
	return 0
}

// unixPtrNotice 秒级时间戳转 *time.Time, 0 返回 nil.
func unixPtrNotice(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0)
	return &t
}

// timeOrUnixNotice *time.Time 转秒级时间戳, nil 返回 0.
func timeOrUnixNotice(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
