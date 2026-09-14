// Package model 定义 workorder-service 的 GORM 数据模型(对齐 docs/M2设计文档V1.1 第七章工单域).
// 约定: 每服务独立库(workorder_db), 禁止跨库 JOIN; 同库内可用 GORM 关联.
// 所有业务表统一携带 tenant_id(RBAC 数据隔离) 与软删除/时间戳; 工单主表额外携带 version(乐观锁).
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

// WorkOrder 工单主表, 覆盖报修/投诉/巡检/保洁/装修/搬运等物业工单.
// 状态机见 internal/state 包; version 字段用于 GORM 乐观锁防并发派单/流转.
type WorkOrder struct {
	BaseModel
	OrderNo     string    `gorm:"column:order_no;type:varchar(32);not null;uniqueIndex" json:"order_no"` // 工单号 WO-YYYYMMDD-0001
	Type        int8      `gorm:"column:type;not null" json:"type"`                                     // 1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他
	Title       string    `gorm:"column:title;type:varchar(128);not null" json:"title"`
	Description string    `gorm:"column:description;type:varchar(1024)" json:"description"`
	ReporterID  int64     `gorm:"column:reporter_id;not null" json:"reporter_id"`                       // 报修/发起人
	AssigneeID  int64     `gorm:"column:assignee_id;not null;default:0" json:"assignee_id"`              // 处理人, 未派单为0
	DepartmentID int64    `gorm:"column:department_id;not null;default:0" json:"department_id"`          // 处理部门
	Status      int8      `gorm:"column:status;not null;default:0;index:idx_status" json:"status"`       // 0待派单 1处理中 2待验收 3已完成 4已关闭
	Priority    int8      `gorm:"column:priority;not null;default:2" json:"priority"`                   // 1紧急 2普通 3低
	Location    string    `gorm:"column:location;type:varchar(128)" json:"location"`
	Attachments string    `gorm:"column:attachments;type:varchar(1024)" json:"attachments"`             // MinIO 对象Key列表(JSON)
	Version     int64     `gorm:"column:version;not null;default:0" json:"version"`                     // 乐观锁版本
	FinishedAt  *time.Time `gorm:"column:finished_at" json:"finished_at"`                               // 完成/关闭时间
}

// TableName 指定工单主表名(对齐 workorder_db 库).
func (WorkOrder) TableName() string { return "work_order" }

// WorkOrderFlow 工单操作流水, 记录每次状态流转(派单/提交/验收/驳回/关闭)用于审计.
type WorkOrderFlow struct {
	BaseModel
	WorkOrderID int64  `gorm:"column:work_order_id;not null;index:idx_wo" json:"work_order_id"`
	FromStatus  int8   `gorm:"column:from_status" json:"from_status"`     // 流转前状态
	ToStatus    int8   `gorm:"column:to_status;not null" json:"to_status"` // 流转后状态
	Action      string `gorm:"column:action;type:varchar(32);not null" json:"action"`
	OperatorID  int64  `gorm:"column:operator_id;not null" json:"operator_id"` // 操作人(网关注入的 x-user-id)
	Remark      string `gorm:"column:remark;type:varchar(512)" json:"remark"`
}

// TableName 指定工单流水表名.
func (WorkOrderFlow) TableName() string { return "work_order_flow" }

// WorkOrderAttachment 工单 MinIO 附件映射, 数据库仅存对象Key, 文件实体存 MinIO.
type WorkOrderAttachment struct {
	BaseModel
	WorkOrderID int64  `gorm:"column:work_order_id;not null;index:idx_wo" json:"work_order_id"`
	ObjectKey   string `gorm:"column:object_key;type:varchar(256);not null" json:"object_key"` // MinIO 对象Key
	FileName    string `gorm:"column:file_name;type:varchar(256)" json:"file_name"`
}

// TableName 指定工单附件表名.
func (WorkOrderAttachment) TableName() string { return "work_order_attachment" }
