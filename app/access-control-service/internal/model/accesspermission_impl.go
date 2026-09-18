package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// permissionModel 是 PermissionModel 的 GORM 实现.
type permissionModel struct {
	db *gorm.DB
}

// NewPermissionModel 构造基于 GORM 的权限数据访问实现.
func NewPermissionModel(db *gorm.DB) PermissionModel {
	return &permissionModel{db: db}
}

// Grant 批量写入权限(MySQL INSERT ... ON DUPLICATE KEY UPDATE).
// 同一 (tenant_id, person_id, device_id) 重复授权(含撤销后重新授权)更新为新权限而非报错,
// 保证 #45 幂等: 管理员重复提交不会产生重复行或唯一键冲突.
func (m *permissionModel) Grant(ctx context.Context, permissions []*AccessPermission) (int64, error) {
	if len(permissions) == 0 {
		return 0, nil
	}
	res := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "person_id"}, {Name: "device_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"time_window", "whitelist", "expire_at", "status", "updated_at",
		}),
	}).Create(permissions)
	if res.Error != nil {
		return 0, res.Error
	}
	// RowsAffected 在 ON DUPLICATE KEY UPDATE 下语义不稳定(更新行可能返回 2), 故以处理条数为准.
	return int64(len(permissions)), nil
}

func (m *permissionModel) Revoke(ctx context.Context, tenantID int64, personIDs []int64, deviceIDs []string) (int64, error) {
	if len(personIDs) == 0 || len(deviceIDs) == 0 {
		return 0, nil
	}
	res := m.db.WithContext(ctx).
		Where("tenant_id = ? AND person_id IN ? AND device_id IN ?", tenantID, personIDs, deviceIDs).
		Delete(&AccessPermission{})
	// Delete 为物理删除: 契约(§2.2)要求撤销即移除记录, 保留历史由访问日志/回收专人负责.
	return res.RowsAffected, res.Error
}

// FindEffective 查询人员×设备的有效授权, 不存在时返回 (nil, nil) 而非错误.
//
// 为什么单独提供: 授权此前只写不读 —— access_permission 有数据但没有任何判定逻辑引用它,
// 授权因此不产生任何约束力。远程开门需要"操作人是否被授权开这扇门"作为放行依据之一.
func (m *permissionModel) FindEffective(ctx context.Context, tenantID, personID int64, deviceID string) (*AccessPermission, error) {
	var p AccessPermission
	err := m.db.WithContext(ctx).
		Where("tenant_id = ? AND person_id = ? AND device_id = ? AND status = ?",
			tenantID, personID, deviceID, PermissionStatusValid).
		First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// AccessRecord 通行记录表(access_db.access_record).
// 由门禁设备上行事件写入(M1 Kafka 事件), result/open_type 取值见 OpenTypeXXX 常量.
type AccessRecord struct {
	ID       int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID int64  `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`
	PersonID int64  `gorm:"column:person_id;not null;default:0" json:"person_id"`
	DeviceID string `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	// 注意: 此处不可加 default 标签 —— GORM 创建时会跳过带 default 的零值字段,
	// 导致 result=0(通行失败)被省略后取列默认值 1(成功), 失败记录将永远落不了库.
	Result     int8      `gorm:"column:result;not null" json:"result"`
	OpenType   string    `gorm:"column:open_type;type:varchar(16);not null;default:''" json:"open_type"`
	FailReason string    `gorm:"column:fail_reason;type:varchar(128);not null;default:''" json:"fail_reason"`
	CreatedAt  time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

// TableName 指定通行记录表名.
func (AccessRecord) TableName() string { return "access_record" }

// 通行结果取值(docs/m3/04 §5.4).
const (
	AccessResultFail    int8 = 0 // 通行失败
	AccessResultSuccess int8 = 1 // 通行成功
)

// 开门方式取值(open_type).
const (
	OpenTypeCard   = "card"   // 刷卡
	OpenTypeFace   = "face"   // 人脸
	OpenTypeRemote = "remote" // 远程开门
	OpenTypeQRCode = "qrcode" // 访客二维码
)

// AccessRecordFilter 通行记录筛选条件, 零值字段表示不参与筛选.
type AccessRecordFilter struct {
	TenantID  int64
	PersonID  int64  // 0 表示不筛选
	DeviceID  string // 空表示不筛选
	Result    *int8  // nil 表示不筛选
	OpenType  string // 空表示不筛选
	StartTime *time.Time
	EndTime   *time.Time
	Page      int // 从 1 开始
	PageSize  int
}

// RecordModel 通行记录数据访问层, 接口化以便单测替换.
type RecordModel interface {
	// List 分页查询通行记录, 按 created_at DESC 排序.
	List(ctx context.Context, f AccessRecordFilter) ([]*AccessRecord, int64, error)
	// Create 写入一条通行记录(远程开门等已发生的通行事实).
	Create(ctx context.Context, r *AccessRecord) error
}

type recordModel struct {
	db *gorm.DB
}

// NewRecordModel 构造基于 GORM 的通行记录数据访问实现.
func NewRecordModel(db *gorm.DB) RecordModel {
	return &recordModel{db: db}
}

func (m *recordModel) Create(ctx context.Context, r *AccessRecord) error {
	return m.db.WithContext(ctx).Create(r).Error
}

func (m *recordModel) List(ctx context.Context, f AccessRecordFilter) ([]*AccessRecord, int64, error) {
	tx := m.db.WithContext(ctx).Model(&AccessRecord{}).Where("tenant_id = ?", f.TenantID)
	if f.PersonID != 0 {
		tx = tx.Where("person_id = ?", f.PersonID)
	}
	if f.DeviceID != "" {
		tx = tx.Where("device_id = ?", f.DeviceID)
	}
	if f.Result != nil {
		tx = tx.Where("result = ?", *f.Result)
	}
	if f.OpenType != "" {
		tx = tx.Where("open_type = ?", f.OpenType)
	}
	if f.StartTime != nil {
		tx = tx.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		tx = tx.Where("created_at <= ?", *f.EndTime)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*AccessRecord
	if err := tx.Order("created_at DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// normalizePage 修正非法分页参数: 页码从 1 开始, 页大小默认 10 且上限 100.
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
