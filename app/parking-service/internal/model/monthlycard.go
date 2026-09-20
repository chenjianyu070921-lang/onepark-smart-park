// Package model — 停车月卡(P2 场景: 月卡车辆管理/识别/到期提醒).
package model

import (
	"time"

	"onepark/common/gormx"
)

// MonthlyCard 月卡车辆: 一条 = 某车牌一段有效期的月卡.
// 识别口径: 车辆入场时按 (tenant_id, plate_no) 查 status=生效 且 start_time<=入场时刻<=end_time;
// 命中则该次停车按月卡计费(fee=0). 停用/过期月卡不参与识别.
type MonthlyCard struct {
	BaseModel
	PlateNo   string    `gorm:"column:plate_no;type:varchar(32);not null;index:idx_tenant_plate,priority:2" json:"plate_no"` // 车牌号
	OwnerName string    `gorm:"column:owner_name;type:varchar(64);default:''" json:"owner_name"`                             // 车主姓名
	Phone     string    `gorm:"column:phone;type:varchar(20);default:''" json:"phone"`                                       // 联系电话
	StartTime time.Time `gorm:"column:start_time;not null" json:"start_time"`                                                // 生效起
	EndTime   time.Time `gorm:"column:end_time;not null" json:"end_time"`                                                    // 生效止(到期时间)
	Status    int8      `gorm:"column:status;not null;default:1" json:"status"`                                              // 1生效 2停用
}

// TableName 指定月卡表名(对齐 parking_db 库).
func (MonthlyCard) TableName() string { return "monthly_card" }

// 月卡状态码(与 MonthlyCard.Status 字段含义一致)
const (
	MonthlyCardStatusActive   int8 = 1 // 生效
	MonthlyCardStatusDisabled int8 = 2 // 停用
)

// HasActiveMonthlyCard 判断车牌在 at 时刻是否有生效月卡(月卡车辆识别的核心判定).
// 入参: db 数据库连接(nil 时降级返回 false, 车辆按临时车计费, 不阻断入场);
// tenantID 园区ID, plateNo 车牌号, at 判定时刻(入场事件时间).
func HasActiveMonthlyCard(db *gormx.DB, tenantID int64, plateNo string, at time.Time) bool {
	if db == nil {
		return false
	}
	var n int64
	err := db.Model(&MonthlyCard{}).
		Where("tenant_id=? AND plate_no=? AND status=? AND start_time<=? AND end_time>=?",
			tenantID, plateNo, MonthlyCardStatusActive, at, at).
		Count(&n).Error
	return err == nil && n > 0
}
