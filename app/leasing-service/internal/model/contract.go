// Package model 定义 leasing-service 的 GORM 数据模型(leasing_db).
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// 合同状态, 与 lease_contract.status 取值一致.
const (
	StatusPending    int8 = 1 // 待生效
	StatusActive     int8 = 2 // 生效中
	StatusExpired    int8 = 3 // 已到期
	StatusTerminated int8 = 4 // 已终止
)

// 账单状态, 与 lease_bill.status 取值一致.
const (
	BillStatusUnpaid int8 = 1 // 未缴
	BillStatusPaid   int8 = 2 // 已缴
)

// 自动续约开关. 默认关闭: 自动延长租期等于替承租方做决定, 必须由合同条款显式约定.
const (
	AutoRenewOff int8 = 0
	AutoRenewOn  int8 = 1
)

// DefaultRenewNoticeDays 默认"到期前多少天进入续签提醒窗口".
const DefaultRenewNoticeDays int64 = 30

// LeaseContract 租赁合同.
// 金额字段一律用 decimal.Decimal, 禁止 float64 —— 浮点累加会产生精度误差.
type LeaseContract struct {
	Id          int64           `gorm:"column:id;primaryKey;autoIncrement"`
	ContractNo  string          `gorm:"column:contract_no;size:32;uniqueIndex"`
	TenantId    int64           `gorm:"column:tenant_id"`
	TenantName  string          `gorm:"column:tenant_name;size:128"`
	ZoneCode    string          `gorm:"column:zone_code;size:64"`
	AreaSqm     float64         `gorm:"column:area_sqm"`
	MonthlyRent decimal.Decimal `gorm:"column:monthly_rent"`
	Deposit     decimal.Decimal `gorm:"column:deposit"`
	StartDate   time.Time       `gorm:"column:start_date"`
	EndDate     time.Time       `gorm:"column:end_date"`
	Status      int8            `gorm:"column:status"`
	// AutoRenew 约定自动续约时才为 1; 到期时由定时任务按原租期长度顺延。
	AutoRenew int8 `gorm:"column:auto_renew"`
	// RenewNoticeDays 到期前多少天进入续签提醒窗口(仅影响提醒, 不影响状态流转)。
	RenewNoticeDays int64 `gorm:"column:renew_notice_days"`
	Version     int64           `gorm:"column:version"`
	CreatedAt   time.Time       `gorm:"column:created_at"`
	UpdatedAt   time.Time       `gorm:"column:updated_at"`
}

// TableName 指定表名.
func (LeaseContract) TableName() string { return "lease_contract" }

// LeaseZone 园区可租区域, 用于计算入驻率(已租面积 / 总面积).
type LeaseZone struct {
	Id           int64     `gorm:"column:id;primaryKey;autoIncrement"`
	ZoneCode     string    `gorm:"column:zone_code;size:64;uniqueIndex"`
	ZoneName     string    `gorm:"column:zone_name;size:128"`
	TotalAreaSqm float64   `gorm:"column:total_area_sqm"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

// TableName 指定表名.
func (LeaseZone) TableName() string { return "lease_zone" }

// LeaseBill 租金账单.
// (contract_id, billing_period) 上的唯一索引是自动生成账单幂等的根基.
type LeaseBill struct {
	Id            int64           `gorm:"column:id;primaryKey;autoIncrement"`
	BillNo        string          `gorm:"column:bill_no;size:32"`
	ContractId    int64           `gorm:"column:contract_id"`
	TenantId      int64           `gorm:"column:tenant_id"`
	BillingPeriod string          `gorm:"column:billing_period;size:7"`
	Amount        decimal.Decimal `gorm:"column:amount"`
	Status        int8            `gorm:"column:status"`
	CreatedAt     time.Time       `gorm:"column:created_at"`
	UpdatedAt     time.Time       `gorm:"column:updated_at"`
}

// TableName 指定表名.
func (LeaseBill) TableName() string { return "lease_bill" }

// LeaseContractStatusLog 合同状态流转审计: 状态机负责"能不能改", 本表负责"改过之后能查".
type LeaseContractStatusLog struct {
	Id         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	ContractId int64     `gorm:"column:contract_id"`
	FromStatus int8      `gorm:"column:from_status"`
	ToStatus   int8      `gorm:"column:to_status"`
	Action     string    `gorm:"column:action;size:32"`
	Reason     string    `gorm:"column:reason;size:512"`
	OperatorId int64     `gorm:"column:operator_id"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

// TableName 指定表名.
func (LeaseContractStatusLog) TableName() string { return "lease_contract_status_log" }
