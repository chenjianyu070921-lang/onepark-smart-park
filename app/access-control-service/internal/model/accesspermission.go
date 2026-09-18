package model

import (
	"context"
	"time"
)

// AccessPermission 门禁权限表(access_db.access_permission).
// TimeWindow 保存 JSON 文本({start,end,days}); 指针为 nil 时写入 NULL(全天无限制),
// 不可写入空串 —— MySQL JSON 列接收空串会报 "Invalid JSON text".
type AccessPermission struct {
	BaseModel
	PersonID   int64   `gorm:"column:person_id;not null;default:0" json:"person_id"`
	DeviceID   string  `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	TimeWindow *string `gorm:"column:time_window;type:json" json:"time_window"`
	Whitelist  int8    `gorm:"column:whitelist;not null;default:0" json:"whitelist"`
	// Status 同样不设 default 标签: status=0(失效)是业务有效取值, 加 default 会被 GORM 跳过写成 1.
	Status   int8       `gorm:"column:status;not null" json:"status"`
	ExpireAt *time.Time `gorm:"column:expire_at" json:"expire_at"`
}

// TableName 指定门禁权限表名.
func (AccessPermission) TableName() string { return "access_permission" }

// 权限状态取值(docs/m3/04 §2.2).
const (
	PermissionStatusInvalid int8 = 0 // 失效
	PermissionStatusValid   int8 = 1 // 有效
)

// PermissionModel 门禁权限数据访问层, 接口化以便单测替换.
type PermissionModel interface {
	// Grant 批量写入权限(person × device 笛卡尔积); 已存在的组合更新为新权限并返回被处理的行数.
	Grant(ctx context.Context, permissions []*AccessPermission) (int64, error)
	// Revoke 按人员+设备删除权限, 返回实际删除行数(0 表示无匹配记录).
	Revoke(ctx context.Context, tenantID int64, personIDs []int64, deviceIDs []string) (int64, error)
	// FindEffective 查询人员在设备上的有效授权(status=1); 不存在返回 (nil, nil).
	// 只按 status 过滤: 过期时间与时间段需要在给定时刻判定, 交给 logic 层以便注入时间做单测.
	FindEffective(ctx context.Context, tenantID, personID int64, deviceID string) (*AccessPermission, error)
}
