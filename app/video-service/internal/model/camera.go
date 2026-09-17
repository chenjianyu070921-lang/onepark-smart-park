// Package model 定义 video-service 的 GORM 数据模型.
// 对齐 docs/m3/04-接口与数据契约.md §2.3 的 video_db.camera 表结构.
package model

import (
	"context"
	"errors"
	"time"
)

// ErrCameraNotFound 摄像头不存在.
var ErrCameraNotFound = errors.New("video: camera not found")

// 摄像头状态取值(docs/m3/04 §5.4).
const (
	CameraStatusOffline int8 = 0 // 离线
	CameraStatusOnline  int8 = 1 // 在线
	CameraStatusFault   int8 = 2 // 故障
)

// Camera 摄像头表(video_db.camera).
// Location 保存 JSON 文本({lng,lat,floor}); 指针为 nil 时写入 NULL,
// 不可写空串 —— MySQL JSON 列接收空串会报 "Invalid JSON text".
// Status/Location 均不带 default 标签: 否则 status=0(离线)会被 GORM 跳过取列默认值.
type Camera struct {
	ID              int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID        int64      `gorm:"column:tenant_id;not null;index:idx_tenant" json:"tenant_id"`
	Name            string     `gorm:"column:name;type:varchar(64);not null;default:''" json:"name"`
	DeviceID        string     `gorm:"column:device_id;type:varchar(64);not null;default:''" json:"device_id"`
	AreaID          int64      `gorm:"column:area_id;not null;default:0" json:"area_id"`
	RtspURL         string     `gorm:"column:rtsp_url;type:varchar(255);not null;default:''" json:"rtsp_url"`
	Location        *string    `gorm:"column:location;type:json" json:"location"`
	Status          int8       `gorm:"column:status;not null" json:"status"`
	LastHeartbeatAt *time.Time `gorm:"column:last_heartbeat_at" json:"last_heartbeat_at"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

// TableName 指定摄像头表名.
func (Camera) TableName() string { return "camera" }

// CameraListFilter 摄像头列表筛选条件, 零值字段表示不参与筛选.
type CameraListFilter struct {
	TenantID int64
	AreaID   int64 // 0 表示全部区域
	Status   *int8 // nil 表示全部状态
	Page     int   // 从 1 开始
	PageSize int
}

// CameraModel 摄像头数据访问层, 接口化以便单测替换.
type CameraModel interface {
	// Create 写入摄像头; (tenant_id, device_id) 冲突时返回 ErrCameraDuplicate.
	Create(ctx context.Context, c *Camera) error
	// FindByID 按租户+主键查询, 不存在返回 ErrCameraNotFound.
	FindByID(ctx context.Context, tenantID, id int64) (*Camera, error)
	// List 分页查询, 按 id DESC 排序.
	List(ctx context.Context, f CameraListFilter) ([]*Camera, int64, error)
}
