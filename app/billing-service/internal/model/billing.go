package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

const (
	// RuleTypeFlat 单一电价
	RuleTypeFlat = 1
	// RuleTypeTiered 阶梯电价
	RuleTypeTiered = 2
	// RuleTypeTou 峰谷分时电价
	RuleTypeTou = 3

	// RuleStatusOn 启用
	RuleStatusOn = 1
	// RuleStatusOff 停用
	RuleStatusOff = 0

	// BillStatusUnpaid 待缴
	BillStatusUnpaid = 1
	// BillStatusPaid 已缴
	BillStatusPaid = 2
	// BillStatusVoid 作废
	BillStatusVoid = 3
)

// BillingRule 对应表 billing_rule, 一条规则 = 某个区域一度电怎么算钱
type BillingRule struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	Name       string    `gorm:"column:name;type:varchar(64);not null" json:"name"`
	ZoneID     string    `gorm:"column:zone_id;type:varchar(64);not null;default:''" json:"zoneId"`
	RuleType   int64     `gorm:"column:rule_type;not null;default:1" json:"ruleType"`
	ConfigJSON string    `gorm:"column:config;type:json;not null" json:"-"` // 规则的 JSON 原文, 解析交给上层
	Status     int64     `gorm:"column:status;not null;default:1" json:"status"`
	CreatedAt  time.Time `gorm:"column:created_at;type:datetime;autoCreateTime" json:"createdAt"`
	UpdatedAt  time.Time `gorm:"column:updated_at;type:datetime;autoUpdateTime" json:"updatedAt"`
}

func (BillingRule) TableName() string { return "billing_rule" }

// Bill 对应表 bill, 一行 = 某区域某个月的一张账单
type Bill struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement;column:id" json:"id"`
	BillNo       string     `gorm:"column:bill_no;type:varchar(32);not null;default:''" json:"billNo"`
	ZoneID       string     `gorm:"column:zone_id;type:varchar(64);not null;default:''" json:"zoneId"`
	RuleID       uint64     `gorm:"column:rule_id;not null;default:0" json:"ruleId"`
	RuleSnapshot string     `gorm:"column:rule_snapshot;type:json" json:"-"` // 出账时的规则快照
	Detail       string     `gorm:"column:detail;type:json" json:"-"`        // 计费明细(JSON), 说明这钱怎么算出来的
	PeriodStart  time.Time  `gorm:"column:period_start;type:date;not null" json:"periodStart"`
	PeriodEnd    time.Time  `gorm:"column:period_end;type:date;not null" json:"periodEnd"`
	UsageKwh     float64    `gorm:"column:usage_kwh;type:double;not null;default:0" json:"usageKwh"`
	Amount       float64    `gorm:"column:amount;type:decimal(12,2);not null;default:0" json:"amount"`
	Status       int64      `gorm:"column:status;not null;default:1" json:"status"`
	PaidAt       *time.Time `gorm:"column:paid_at;type:datetime" json:"paidAt"`
	CreatedAt    time.Time  `gorm:"column:created_at;type:datetime;autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;type:datetime;autoUpdateTime" json:"updatedAt"`
}

func (Bill) TableName() string { return "bill" }

type BillingModel struct {
	db *gorm.DB
}

func NewBillingModel(db *gorm.DB) *BillingModel {
	return &BillingModel{db: db}
}

// InsertRule 新增一条计费规则
func (m *BillingModel) InsertRule(ctx context.Context, r *BillingRule) error {
	return m.db.WithContext(ctx).Create(r).Error
}

// FindRule 按 id 查规则
func (m *BillingModel) FindRule(ctx context.Context, id uint64) (*BillingRule, error) {
	var r BillingRule
	err := m.db.WithContext(ctx).Where("id = ?", id).First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRule 查规则列表, zoneID 为空表示不限区域, status 传 -1 表示不限状态
func (m *BillingModel) ListRule(ctx context.Context, zoneID string, status int64) ([]BillingRule, error) {
	q := m.db.WithContext(ctx)
	if zoneID != "" {
		q = q.Where("zone_id = ?", zoneID)
	}
	if status >= 0 {
		q = q.Where("status = ?", status)
	}
	var list []BillingRule
	err := q.Order("id DESC").Find(&list).Error
	return list, err
}

// FindApplicableRule 找出某区域能用的规则: 优先该区域自己的, 没有就用全园区默认(zone_id='')
func (m *BillingModel) FindApplicableRule(ctx context.Context, zoneID string) (*BillingRule, error) {
	var r BillingRule
	err := m.db.WithContext(ctx).
		Where("zone_id = ? AND status = ?", zoneID, RuleStatusOn).
		Order("id DESC").First(&r).Error
	if err == nil {
		return &r, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	// 该区域没配专属规则, 退回全园区默认规则
	err = m.db.WithContext(ctx).
		Where("zone_id = '' AND status = ?", RuleStatusOn).
		Order("id DESC").First(&r).Error
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FindBillByPeriod 查某区域某账期是不是已经出过账(避免重复出账)
func (m *BillingModel) FindBillByPeriod(ctx context.Context, zoneID string, start, end time.Time) (*Bill, error) {
	var b Bill
	err := m.db.WithContext(ctx).
		Where("zone_id = ? AND period_start = ? AND period_end = ?", zoneID, start, end).
		First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// InsertBill 落一条账单
func (m *BillingModel) InsertBill(ctx context.Context, b *Bill) error {
	return m.db.WithContext(ctx).Create(b).Error
}

// ListBill 分页查账单, status 传 -1 表示不限
func (m *BillingModel) ListBill(ctx context.Context, zoneID string, status, page, pageSize int64) ([]Bill, int64, error) {
	q := m.db.WithContext(ctx).Model(&Bill{})
	if zoneID != "" {
		q = q.Where("zone_id = ?", zoneID)
	}
	if status >= 0 {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	var list []Bill
	err := q.Order("id DESC").Offset(int(offset)).Limit(int(pageSize)).Find(&list).Error
	return list, total, err
}
