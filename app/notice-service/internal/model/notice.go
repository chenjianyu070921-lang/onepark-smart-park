// Package model 定义 notice-service 的 GORM 数据模型(对齐 docs/M2设计文档V1.1 第七章公告域).
// 约定: 每服务独立库(notice_db), 禁止跨库 JOIN; 同库内可用 GORM 关联.
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

// Notice 公告/通知, 支持通知/公告/活动/停水/停电等类型, 可置顶与定时发布.
type Notice struct {
	BaseModel
	Title       string `gorm:"column:title;type:varchar(255);not null" json:"title"`
	Content     string `gorm:"column:content;type:text" json:"content"`                       // 公告正文
	Type        int8   `gorm:"column:type;not null;default:1" json:"type"`                    // 1通知 2公告 3活动 4停水 5停电
	PublisherID int64  `gorm:"column:publisher_id;not null;default:0" json:"publisher_id"`    // 发布人(网关注入 x-user-id)
	Top         int8   `gorm:"column:top;not null;default:0" json:"top"`                      // 是否置顶 0否 1是
	Status      int8   `gorm:"column:status;not null;default:1" json:"status"`                // 1草稿 2已发布 3已撤回
	PublishAt   *time.Time `gorm:"column:publish_at" json:"publish_at"`                       // 定时发布时间, NULL=立即
}

// TableName 指定公告表名(对齐 notice_db 库).
func (Notice) TableName() string { return "notice" }

// NoticeRead 公告已读记录, 用于已读统计(Redis 辅助).
type NoticeRead struct {
	BaseModel
	NoticeID int64 `gorm:"column:notice_id;not null;index:idx_notice_user" json:"notice_id"` // 关联公告
	UserID   int64 `gorm:"column:user_id;not null" json:"user_id"`                          // 已读用户
	ReadAt   *time.Time `gorm:"column:read_at" json:"read_at"`                              // 已读时间
}

// TableName 指定公告已读表名.
func (NoticeRead) TableName() string { return "notice_read" }

// 公告状态码(与 Notice.Status 字段含义一致)
const (
	NoticeStatusDraft    int8 = 1 // 草稿
	NoticeStatusPublished int8 = 2 // 已发布
	NoticeStatusWithdrawn int8 = 3 // 已撤回
)

// 公告类型(与 Notice.Type 字段含义一致)
const (
	NoticeTypeNotify    int8 = 1 // 通知
	NoticeTypeAnnounce  int8 = 2 // 公告
	NoticeTypeActivity  int8 = 3 // 活动
	NoticeTypeWaterStop int8 = 4 // 停水
	NoticeTypePowerStop int8 = 5 // 停电
)
