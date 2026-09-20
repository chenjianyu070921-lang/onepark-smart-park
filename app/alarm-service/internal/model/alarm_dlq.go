package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrDLQNotFound 死信记录不存在(或不属于当前租户).
var ErrDLQNotFound = errors.New("alarm: dead letter not found")

// 死信台账状态(docs/m3/06 §5.3).
const (
	DLQStatusPending  int8 = 0 // 待处理
	DLQStatusReplayed int8 = 1 // 已重放
	DLQStatusDropped  int8 = 2 // 已丢弃(人工判定无效)
)

// AlarmDLQ 消费死信台账(alarm_db.alarm_dlq).
// 记录最终处理失败的消息, 便于排查与人工重放; 不参与告警主流程.
type AlarmDLQ struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID    int64     `gorm:"column:tenant_id;not null;default:0" json:"tenant_id"`
	Topic       string    `gorm:"column:topic;type:varchar(64);not null;default:''" json:"topic"`
	PartitionNo int       `gorm:"column:partition_no;not null;default:0" json:"partition_no"`
	MsgOffset   int64     `gorm:"column:msg_offset;not null;default:0" json:"msg_offset"`
	RequestID   string    `gorm:"column:request_id;type:varchar(64);not null;default:''" json:"request_id"`
	DeviceID    string    `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	EventType   string    `gorm:"column:event_type;type:varchar(64);not null;default:''" json:"event_type"`
	Payload     string    `gorm:"column:payload;type:text" json:"payload"`
	ErrorMsg    string    `gorm:"column:error_msg;type:varchar(512);not null;default:''" json:"error_msg"`
	RetryCount  int       `gorm:"column:retry_count;not null;default:0" json:"retry_count"`
	Status      int8      `gorm:"column:status;not null;default:0" json:"status"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// TableName 指定死信台账表名.
func (AlarmDLQ) TableName() string { return "alarm_dlq" }

// DeadLetterListFilter 死信列表筛选条件, 零值字段表示不参与筛选.
type DeadLetterListFilter struct {
	TenantID int64
	Status   *int8  // nil 表示不筛选; 0 待处理 / 1 已重放 / 2 已丢弃
	DeviceID string // 空表示全部设备
	Page     int    // 从 1 开始
	PageSize int
}

// DeadLetterModel 死信台账数据访问层, 接口化以便单测替换.
type DeadLetterModel interface {
	// Create 写入一条死信记录.
	Create(ctx context.Context, d *AlarmDLQ) error
	// FindByID 按租户+主键查询, 不存在返回 ErrDLQNotFound.
	FindByID(ctx context.Context, tenantID, id int64) (*AlarmDLQ, error)
	// List 分页查询死信, 按 created_at DESC 排序.
	List(ctx context.Context, f DeadLetterListFilter) ([]*AlarmDLQ, int64, error)
	// MarkReplayed 将死信置为已重放; RowsAffected 为 0(不存在或非本租户)返回 ErrDLQNotFound.
	MarkReplayed(ctx context.Context, tenantID, id int64) error
}

type deadLetterModel struct {
	db *gorm.DB
}

// NewDeadLetterModel 构造基于 GORM 的死信台账实现.
func NewDeadLetterModel(db *gorm.DB) DeadLetterModel {
	return &deadLetterModel{db: db}
}

func (m *deadLetterModel) Create(ctx context.Context, d *AlarmDLQ) error {
	return m.db.WithContext(ctx).Create(d).Error
}

func (m *deadLetterModel) FindByID(ctx context.Context, tenantID, id int64) (*AlarmDLQ, error) {
	var d AlarmDLQ
	err := m.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDLQNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (m *deadLetterModel) List(ctx context.Context, f DeadLetterListFilter) ([]*AlarmDLQ, int64, error) {
	tx := m.db.WithContext(ctx).Model(&AlarmDLQ{}).Where("tenant_id = ?", f.TenantID)
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}
	if f.DeviceID != "" {
		tx = tx.Where("device_id = ?", f.DeviceID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := normalizePage(f.Page, f.PageSize)
	var list []*AlarmDLQ
	if err := tx.Order("created_at DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (m *deadLetterModel) MarkReplayed(ctx context.Context, tenantID, id int64) error {
	// 用 map 而非 struct 更新: GORM 用 struct 会跳过零值字段.
	res := m.db.WithContext(ctx).Model(&AlarmDLQ{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(map[string]interface{}{"status": DLQStatusReplayed, "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrDLQNotFound
	}
	return nil
}
