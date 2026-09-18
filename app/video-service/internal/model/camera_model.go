package model

import (
	"context"
	"errors"
	"strings"
	"time"

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

// Update 按租户+主键增量更新. 只写 patch 中非 nil 的字段,
// 未提供的字段保持原值 —— 否则调用方只想改 rtsp 却把 location 清空了.
func (m *cameraModel) Update(ctx context.Context, tenantID, id int64, patch CameraPatch) error {
	fields := map[string]interface{}{}
	if patch.Name != nil {
		fields["name"] = *patch.Name
	}
	if patch.AreaID != nil {
		fields["area_id"] = *patch.AreaID
	}
	if patch.RtspURL != nil {
		fields["rtsp_url"] = *patch.RtspURL
	}
	if patch.Location != nil {
		fields["location"] = *patch.Location
	}
	if patch.Status != nil {
		fields["status"] = *patch.Status
	}
	if len(fields) == 0 {
		// 空更新视为无操作而非"全量清空": 调用方多半是参数解析出错, 报错比静默改数据好.
		return ErrCameraNotFound
	}
	fields["updated_at"] = time.Now()

	res := m.db.WithContext(ctx).Model(&Camera{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// Delete 地址簿下架. 采用物理删除: camera 只承载"当前在用的摄像机地址簿",
// 无历史流记录引用它; 若将来需要保留审计, 应改为软删而不是在这里留半套逻辑.
func (m *cameraModel) Delete(ctx context.Context, tenantID, id int64) error {
	res := m.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&Camera{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCameraNotFound
	}
	return nil
}

// TouchHeartbeat 按设备ID登记心跳.
//
// 跨租户约束: M1 的遥测报文不带 tenant_id(见 app/event-dispatcher 的 Message 结构),
// 只能按 device_id 反查. 若同一 device_id 在多个租户下都存在, 逐条更新会写成"某园区的心跳
// 更新到别的园区摄像头上", 因此只在全局唯一命中时才写入, 否则回 false 让调用方告警.
func (m *cameraModel) TouchHeartbeat(ctx context.Context, deviceID string, at time.Time) (int, error) {
	var ids []int64
	if err := m.db.WithContext(ctx).Model(&Camera{}).
		Where("device_id = ?", deviceID).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	switch len(ids) {
	case 0:
		return 0, nil
	case 1:
		res := m.db.WithContext(ctx).Model(&Camera{}).
			Where("id = ?", ids[0]).
			Updates(map[string]interface{}{
				"last_heartbeat_at": at,
				"status":            CameraStatusOnline,
				"updated_at":        at,
			})
		if res.Error != nil {
			return 0, res.Error
		}
		return len(ids), nil
	default:
		// 不写入: 无法判断这条心跳属于哪个园区, 写任何一个都是把 A 园区的在线状态安到 B 园区头上.
		return len(ids), nil
	}
}

// MarkOffline 心跳超时转离线. 只扫 status=1 的行: 故障态(2)由人工排障后恢复,
// 不能被"没心跳"自动洗成离线, 否则故障会被静默抹掉.
func (m *cameraModel) MarkOffline(ctx context.Context, deadline time.Time) (int64, error) {
	res := m.db.WithContext(ctx).Model(&Camera{}).
		Where("status = ? AND (last_heartbeat_at IS NULL OR last_heartbeat_at < ?)", CameraStatusOnline, deadline).
		Updates(map[string]interface{}{
			"status":     CameraStatusOffline,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
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
