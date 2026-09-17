// Package model 定义 access-control-service 的 GORM 数据模型(门禁域).
// 约定: 业务表统一携带 tenant_id(RBAC 数据隔离) 与时间戳; 不建物理外键, 仅逻辑关联.
// 建表 SQL 见 deploy/sql/accesscontrol_mysql_tables.sql (逐列对齐).
package model

import "time"

// ParkingGate 门禁/道闸点位表(parking_gate): 园区内可控门禁点位与 M1 设备的映射.
// 远程开门时按点位查到 device_id, 经 M1 device-service gRPC SendCommand 下发指令.
// 说明: 命名沿用组长任务口径(ParkingGate), 语义覆盖 门禁闸机 与 停车道闸 两类可控点位.
type ParkingGate struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID  int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`               // 园区ID, RBAC 数据隔离维度
	Name      string    `gorm:"column:name;type:varchar(64);not null" json:"name"`                         // 点位名称, 如 东门道闸/1号楼单元门
	Location  string    `gorm:"column:location;type:varchar(128);default:''" json:"location"`              // 安装位置
	DeviceID  string    `gorm:"column:device_id;type:varchar(64);not null" json:"device_id"`               // M1 设备中心注册的 device_id(SendCommand 下发目标)
	Command   string    `gorm:"column:command;type:varchar(32);not null;default:open_door" json:"command"` // 开门指令名, 默认 open_door
	Status    int8      `gorm:"column:status;not null;default:1" json:"status"`                            // 1启用 0停用
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// TableName 指定门禁点位表名.
func (ParkingGate) TableName() string { return "parking_gate" }

// AccessRecord 门禁通行/远程开门记录表(access_record): 每次远程开门一条(成功/失败均落), 审计可溯.
type AccessRecord struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID   int64     `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`   // 园区ID, RBAC 数据隔离维度
	GateID     int64     `gorm:"column:gate_id;not null;index:idx_gate_created" json:"gate_id"` // 门禁点位ID(parking_gate.id)
	DeviceID   string    `gorm:"column:device_id;type:varchar(64);default:''" json:"device_id"` // 实际执行指令的 M1 设备ID
	Action     string    `gorm:"column:action;type:varchar(32);not null" json:"action"`         // 动作: remote_open 远程开门
	Result     int8      `gorm:"column:result;not null" json:"result"`                          // 1成功 0失败
	OperatorID int64     `gorm:"column:operator_id;not null;default:0" json:"operator_id"`      // 操作人(user_id), 0=系统
	Remark     string    `gorm:"column:remark;type:varchar(255);default:''" json:"remark"`      // 开门事由/失败原因
	CreatedAt  time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

// TableName 指定通行记录表名.
func (AccessRecord) TableName() string { return "access_record" }

// 门禁点位状态码(与 ParkingGate.Status 字段含义一致)
const (
	GateStatusEnabled  int8 = 1 // 启用: 允许远程开门
	GateStatusDisabled int8 = 0 // 停用: 拒绝远程开门
)

// 通行记录结果码(与 AccessRecord.Result 字段含义一致)
const (
	RecordResultFail    int8 = 0 // 指令下发失败/设备拒绝
	RecordResultSuccess int8 = 1 // 开门成功
)

// 通行记录动作(与 AccessRecord.Action 字段含义一致)
const (
	ActionRemoteOpen = "remote_open" // 远程开门(预留 qr_pass 扫码通行等扩展)
)
