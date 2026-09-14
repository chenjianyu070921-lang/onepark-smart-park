// Package model 定义 visitor-service 的 GORM 数据模型(对齐 docs/M2设计文档V1.1 第七章访客域).
// 约定: 每服务独立库(visitor_db), 禁止跨库 JOIN; 同库内可用 GORM 关联.
// 所有业务表统一携带 tenant_id(RBAC 数据隔离) 与时间戳.
package model

import "time"

// BaseModel 业务表公共基础字段: 物理主键 + 租户隔离 + 时间戳.
// TenantID 由网关注入的 x-tenant-id 写入, 作为 RBAC 行级隔离维度.
type BaseModel struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID  int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"` // 园区ID, RBAC 数据隔离维度
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// VisitorRecord 访客通行记录, 业主/物业发起邀请生成二维码, 访客扫码签入(核销+调M1开门), 签出.
// 状态见 Status* 常量; qr_code 唯一, 签入/签出时更新 device_id 记录开门设备.
type VisitorRecord struct {
	BaseModel
	InviterID    int64     `gorm:"column:inviter_id;not null" json:"inviter_id"`                 // 邀请人(业主/物业)
	VisitorName  string    `gorm:"column:visitor_name;type:varchar(64);not null" json:"visitor_name"`
	VisitorPhone string    `gorm:"column:visitor_phone;type:varchar(20);default:''" json:"visitor_phone"`
	VisitTime    *time.Time `gorm:"column:visit_time" json:"visit_time"`                         // 预期到访时间
	ExpireTime   *time.Time `gorm:"column:expire_time" json:"expire_time"`                       // 二维码过期时间
	QRCode       string    `gorm:"column:qr_code;type:varchar(512);not null;uniqueIndex" json:"qr_code"` // 加密二维码内容(含签名+有效期)
	Status       int8      `gorm:"column:status;not null;default:1" json:"status"`              // 1待使用 2已签入 3已签出 4已过期
	CheckinAt    *time.Time `gorm:"column:checkin_at" json:"checkin_at"`
	CheckoutAt   *time.Time `gorm:"column:checkout_at" json:"checkout_at"`
	DeviceID     string    `gorm:"column:device_id;type:varchar(64);default:''" json:"device_id"` // 签入/签出开门设备ID
	Blacklisted  int8      `gorm:"column:blacklisted;not null;default:0" json:"blacklisted"`      // 是否黑名单 0否 1是
}

// TableName 指定访客记录表名(对齐 visitor_db 库).
func (VisitorRecord) TableName() string { return "visitor_record" }

// 访客状态码(与 VisitorRecord.Status 字段含义一致)
const (
	VisitorStatusPending  int8 = 1 // 待使用: 已生成二维码未签入
	VisitorStatusCheckin  int8 = 2 // 已签入
	VisitorStatusCheckout int8 = 3 // 已签出
	VisitorStatusExpired  int8 = 4 // 已过期
)
