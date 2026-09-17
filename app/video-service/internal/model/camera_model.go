package model

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// ErrCameraDuplicate 同一租户下设备ID重复(uk_device 命中).
var ErrCameraDuplicate = errors.New("video: camera device duplicated")

type cameraModel struct {
	db *gorm.DB
}

// NewCameraModel 构造基于 GORM 的摄像头数据访问实现.
func NewCameraModel(db *gorm.DB) CameraModel {
	return &cameraModel{db: db}
}

// Create 写入摄像头. MySQL 唯一键冲突(uk_device)返回 1062, 需转译为业务错误,
// 便于上层区分"重复登记"与其它数据库故障.
func (m *cameraModel) Create(ctx context.Context, c *Camera) error {
	err := m.db.WithContext(ctx).Create(c).Error
	if err != nil {
		if isDuplicateEntry(err) {
			return ErrCameraDuplicate
		}
		return err
	}
	return nil
}

func (m *cameraModel) FindByID(ctx context.Context, tenantID, id int64) (*Camera, error) {
	var c Camera
	err := m.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&c).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCameraNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (m *cameraModel) List(ctx context.Context, f CameraListFilter) ([]*Camera, int64, error) {
	tx := m.db.WithContext(ctx).Model(&Camera{}).Where("tenant_id = ?", f.TenantID)
	if f.AreaID != 0 {
		tx = tx.Where("area_id = ?", f.AreaID)
	}
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*Camera
	if err := tx.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// isDuplicateEntry 判定是否为 MySQL 唯一键冲突(错误码 1062).
// go-sql-driver 的 *mysql.MySQLError 需要按类型断言, 这里同时兼容错误串兜底.
func isDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return strings.Contains(err.Error(), "Duplicate entry")
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
