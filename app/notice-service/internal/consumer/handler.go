package consumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm/clause"

	"onepark/app/notice-service/internal/model"
	"onepark/common/gormx"
)

// WorkorderHandler 把工单事件落成站内通知(notice + notice_read).
type WorkorderHandler struct {
	logx.Logger
	db *gormx.DB
}

// NewWorkorderHandler 构造站内通知处理器.
func NewWorkorderHandler(db *gormx.DB) *WorkorderHandler {
	return &WorkorderHandler{
		Logger: logx.WithContext(context.Background()),
		db:     db,
	}
}

// Handle 解析一条工单事件并落地站内通知.
//
// 幂等由 notice 的 uk_source 唯一索引保证: 同一事件重复投递只会落一条通知.
// notice_read(送达记录)用 (notice_id, user_id) 唯一键 + OnConflict DoNothing,
// 重复消费时不会重复插入.
//
// 返回 nil 表示"本条已处理完(含幂等跳过与坏消息丢弃)", 消费循环据此提交位移.
func (h *WorkorderHandler) Handle(ctx context.Context, value []byte) error {
	ev, err := DecodeWorkOrderEvent(value)
	if err != nil {
		// 坏消息必须"记录后跳过": 若返回 error, 位移不提交, 整个分区会被这一条卡死.
		h.Errorf("[consumer] 丢弃非法工单事件: %v", err)
		return nil
	}
	if h.db == nil {
		return fmt.Errorf("数据库未初始化, 无法生成站内通知")
	}

	draft := buildNoticeDraft(ev)
	now := noticeAt(ev, time.Now())
	publishAt := now

	// 同一事件重复投递 → uk_source 冲突 → 幂等跳过(唯一约束是并发下唯一可信的判重).
	notice := &model.Notice{
		Title:       draft.Title,
		Content:     draft.Content,
		Type:        model.NoticeTypeNotify,
		PublisherID: 0, // 系统通知
		Status:      model.NoticeStatusPublished,
		PublishAt:   &publishAt,
		Source:      &draft.Source,
	}
	notice.TenantID = ev.TenantId
	notice.CreatedAt = now
	notice.UpdatedAt = now
	if err := h.db.WithContext(ctx).Create(notice).Error; err != nil {
		if isDuplicateEntry(err) {
			h.Infof("[consumer] 工单事件已通知, 幂等跳过: source=%s", draft.Source)
			return nil
		}
		return fmt.Errorf("写入站内通知失败: %w", err)
	}

	// 送达记录: 通知按"人"落一行, ReadAt 为 NULL 表示已送达未读.
	// 广播型通知(无明确目标人)不落 notice_read, 由前端按公告全量拉取.
	if len(draft.Targets) > 0 {
		rows := make([]model.NoticeRead, 0, len(draft.Targets))
		for _, uid := range draft.Targets {
			r := model.NoticeRead{NoticeID: notice.ID, UserID: uid}
			r.TenantID = ev.TenantId
			r.CreatedAt = now
			r.UpdatedAt = now
			rows = append(rows, r)
		}
		// OnConflict DoNothing → MySQL INSERT IGNORE: 重复投递不报错也不重复插入.
		if err := h.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&rows).Error; err != nil {
			return fmt.Errorf("写入通知送达记录失败: noticeId=%d, %w", notice.ID, err)
		}
	}

	h.Infof("[consumer] 站内通知已生成: noticeId=%d, event=%s, orderNo=%s, targets=%d",
		notice.ID, ev.Event, ev.OrderNo, len(draft.Targets))
	return nil
}

// isDuplicateEntry 判断是否唯一键冲突(MySQL 1062).
// 通过错误文本匹配而非引入 mysql 驱动类型: gorm 会透出驱动原始错误,
// 其文本形如 `Error 1062: Duplicate entry ... for key 'notice.uk_source'`.
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
