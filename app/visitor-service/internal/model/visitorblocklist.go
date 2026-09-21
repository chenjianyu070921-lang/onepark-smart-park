// Package model 定义 visitor-service 的 GORM 数据模型(对齐 docs/M2设计文档V1.1 第七章访客域).
// 约定: 每服务独立库(visitor_db), 禁止跨库 JOIN; 同库内可用 GORM 关联.
// 所有业务表统一携带 tenant_id(RBAC 数据隔离) 与时间戳.

package model

import "time"

// VisitorBlocklist 访客黑名单: 按手机号/身份证号维度拉黑, 邀请与签入实时拦截.
// 与 visitor_record.blacklisted 的区别: 本表是"人员维度"的禁入名单, 可阻止被拉黑人员
// 用任意新邀请再次入园; visitor_record.blacklisted 仅标记单条记录是否已命中黑名单.
type VisitorBlocklist struct {
	BaseModel
	VisitorName   string     `gorm:"column:visitor_name;type:varchar(64);not null;default:''" json:"visitor_name"` // 访客姓名(便于人工核对)
	Phone         string     `gorm:"column:phone;type:varchar(20);not null;default:''" json:"phone"`               // 手机号(拉黑维度之一)
	IdNo          string     `gorm:"column:id_no;type:varchar(64);not null;default:''" json:"id_no"`               // 身份证号(加密存储, 拉黑维度之一)
	Reason        string     `gorm:"column:reason;type:varchar(512)" json:"reason"`                                // 拉黑原因
	EffectiveFrom *time.Time `gorm:"column:effective_from" json:"effective_from"`                                  // 生效起(NULL=立即)
	EffectiveTo   *time.Time `gorm:"column:effective_to" json:"effective_to"`                                      // 生效止(NULL=永久)
	Status        int8       `gorm:"column:status;not null;default:1" json:"status"`                               // 1生效 2解除
	OperatorID    int64      `gorm:"column:operator_id;not null;default:0" json:"operator_id"`                     // 操作人(user_id)
}

// TableName 指定访客黑名单表名(对齐 deploy/sql/m2_p2_visitor_blocklist.sql).
func (VisitorBlocklist) TableName() string { return "visitor_blocklist" }

// 黑名单状态码(与 VisitorBlocklist.Status 字段含义一致)
const (
	BlocklistStatusActive  int8 = 1 // 生效(禁入)
	BlocklistStatusRemoved int8 = 2 // 解除(允许通行)
)
