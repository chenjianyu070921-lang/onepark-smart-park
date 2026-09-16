package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/workorder-service/internal/model"
	"onepark/app/workorder-service/internal/state"
	"onepark/common/gormx"
	"onepark/common/kafka"
)

// eventPublishTimeout 消费侧发布 workorder-event 的超时, 避免 Kafka 异常拖住消费循环.
const eventPublishTimeout = 2 * time.Second

// AlarmHandler 把告警事件落成报修工单(主表+建单流水同事务).
type AlarmHandler struct {
	logx.Logger
	db       *gormx.DB
	producer *kafka.Producer // 建单成功后向下游(notice/大屏)广播 created 事件, 可为 nil
}

// NewAlarmHandler 构造告警建单处理器. producer 可为 nil(只建单不广播).
func NewAlarmHandler(db *gormx.DB, producer *kafka.Producer) *AlarmHandler {
	return &AlarmHandler{
		Logger:   logx.WithContext(context.Background()),
		db:       db,
		producer: producer,
	}
}

// Handle 解析一条告警消息并幂等建单.
//
// 双唯一键语义, 必须区分处理:
//   - uk_alarm_id 冲突 → 同一告警重复投递/多实例并发消费, 幂等跳过(正常路径);
//   - uk_order_no 冲突 → 随机工单号撞号, 换号重建(最多 maxOrderNoRetries 次).
//
// 主表与建单流水在**同一事务**写入: 流水失败则整单回滚, 不会出现"有单无流水"的审计空洞.
// 返回 nil 表示"本条已处理完(含幂等跳过与坏消息丢弃)", 消费循环据此提交位移.
func (h *AlarmHandler) Handle(ctx context.Context, value []byte, defaultTenantID int64) error {
	evt, err := DecodeAlarm(value)
	if err != nil {
		// 坏消息必须"记录后跳过": 若返回 error, 位移不提交, 整个分区会被这一条卡死.
		h.Errorf("[consumer] 丢弃非法告警消息: %v", err)
		return nil
	}
	if h.db == nil {
		return fmt.Errorf("数据库未初始化, 无法自动建单")
	}

	draft := BuildRepairDraft(evt, defaultTenantID)
	wo := buildOrder(draft, time.Now())

	// 建单重试循环: 仅对 uk_order_no 撞号换号重试; uk_alarm_id 重复与其余错误直接落出.
	var lastErr error
	for attempt := 1; attempt <= model.MaxOrderNoRetries; attempt++ {
		lastErr = h.db.WithContext(ctx).Transaction(func(tx *gormx.DB) error {
			// 工单号在事务内生成: 每次重试换新号, 事务失败(撞号)整体回滚不留半条.
			wo.OrderNo = model.NewOrderNo()
			if err := tx.Create(wo).Error; err != nil {
				return err
			}
			return tx.Create(buildCreateFlow(wo, draft)).Error
		})
		if lastErr == nil {
			break
		}
		// 非唯一键冲突(网络/约束缺失等)重试无意义, 直接落出.
		// 唯一键冲突里只有 uk_order_no 值得换号重试; uk_alarm_id 冲突是幂等场景.
		if !model.IsDuplicateEntry(lastErr) || strings.Contains(lastErr.Error(), "uk_alarm_id") {
			break
		}
		h.Infof("[consumer] 工单号撞号, 换号重试(%d/%d): alarmId=%s", attempt, model.MaxOrderNoRetries, draft.AlarmID)
	}
	if lastErr != nil {
		if isAlarmDuplicated(lastErr) {
			// 重复告警是正常现象(重投/多实例), 按幂等处理而不是报错.
			h.Infof("[consumer] 告警已建单, 幂等跳过: alarmId=%s", draft.AlarmID)
			return nil
		}
		return fmt.Errorf("告警自动建单失败: %w", lastErr)
	}

	h.Infof("[consumer] 告警自动建单成功: orderNo=%s, alarmId=%s, tenant=%d, priority=%d",
		wo.OrderNo, draft.AlarmID, draft.TenantID, draft.Priority)

	// 建单成功后广播 created 事件(尽力而为): 通知渠道/大屏由此感知, 失败不影响已落库的工单.
	h.publishCreated(wo)
	return nil
}

// buildOrder 由草稿构造工单主表实体.
func buildOrder(draft RepairOrderDraft, now time.Time) *model.WorkOrder {
	wo := &model.WorkOrder{
		Type:        draft.Type,
		Title:       draft.Title,
		Description: draft.Description,
		ReporterID:  0, // 系统自动建单, 无人工发起人
		Status:      state.StatusPendingDispatch,
		Priority:    draft.Priority,
		Location:    draft.Location,
		Version:     0,
		AlarmID:     &draft.AlarmID,
	}
	wo.TenantID = draft.TenantID
	wo.CreatedAt = now
	wo.UpdatedAt = now
	return wo
}

// buildCreateFlow 构造建单流水(审计用, action=create 不参与 FSM).
func buildCreateFlow(wo *model.WorkOrder, draft RepairOrderDraft) *model.WorkOrderFlow {
	flow := &model.WorkOrderFlow{
		WorkOrderID: wo.ID,
		FromStatus:  -1, // -1 表示建单, 无前态
		ToStatus:    state.StatusPendingDispatch,
		Action:      state.ActionCreate,
		OperatorID:  0,
		Remark:      fmt.Sprintf("告警自动建单: %s", draft.AlarmID),
	}
	flow.TenantID = draft.TenantID
	flow.CreatedAt = wo.CreatedAt
	flow.UpdatedAt = wo.UpdatedAt
	return flow
}

// isAlarmDuplicated 判断是否 uk_alarm_id 幂等冲突(同告警已建过单).
func isAlarmDuplicated(err error) bool {
	return model.IsDuplicateEntry(err) && strings.Contains(err.Error(), "uk_alarm_id")
}

// publishCreated 向 workorder-event 发布建单事件, 尽力而为.
// 用独立超时的 background ctx: 消费 ctx 随消息结束即取消, 会打断发送.
func (h *AlarmHandler) publishCreated(wo *model.WorkOrder) {
	if h.producer == nil {
		return
	}
	body, err := json.Marshal(map[string]interface{}{
		"event_id":      fmt.Sprintf("alarm-%s", deref(wo.AlarmID)),
		"event":         "created",
		"action":        state.ActionCreate,
		"tenant_id":     wo.TenantID,
		"work_order_id": wo.ID,
		"order_no":      wo.OrderNo,
		"from_status":   -1,
		"to_status":     wo.Status,
		"operator_id":   0,
		"assignee_id":   0,
		"timestamp":     time.Now().Unix(),
	})
	if err != nil {
		h.Errorf("[consumer] marshal workorder-event failed: %v", err)
		return
	}
	pubCtx, cancel := context.WithTimeout(context.Background(), eventPublishTimeout)
	defer cancel()
	if err := h.producer.Publish(pubCtx, kafka.TopicWorkorder, []byte(wo.OrderNo), body); err != nil {
		h.Errorf("[consumer] publish workorder-event failed: orderNo=%s, err=%v", wo.OrderNo, err)
	}
}

// deref 安全解引用字符串指针.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
