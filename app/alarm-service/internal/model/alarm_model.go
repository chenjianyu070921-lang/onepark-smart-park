package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound 告警记录不存在.
var ErrNotFound = errors.New("alarm: record not found")

// AlarmListFilter 告警列表筛选条件, 指针字段为 nil 表示不参与筛选.
type AlarmListFilter struct {
	TenantID int64
	Status   *int8  // nil 不筛选; 指向 AlarmStatusPending 时即活跃告警列表
	Level    *int8  // 按告警等级筛选
	AreaID   int64  // 0 表示全部区域
	DeviceID string // 空表示全部设备
	Page     int    // 从 1 开始
	PageSize int
}

// LevelCount 按告警等级聚合的计数.
type LevelCount struct {
	Level int8
	Total int64
}

// AlarmModel 告警数据访问层, 封装 alarm_db.alarm 的读写.
// 业务层通过 svc.ServiceContext.Alarms 使用, 便于单测替换为内存实现.
type AlarmModel interface {
	// Create 写入一条告警; request_id 命中唯一索引时返回 ErrDuplicateRequest.
	Create(ctx context.Context, a *Alarm) error
	// FindByID 按租户+主键查询, 不存在返回 ErrNotFound.
	FindByID(ctx context.Context, tenantID, id int64) (*Alarm, error)
	// List 分页查询, 按 level DESC, created_at DESC 排序.
	List(ctx context.Context, f AlarmListFilter) ([]*Alarm, int64, error)
	// Ack 确认告警(未处理→已确认)并写审计流水; RowsAffected 为 0 返回 ErrNotFound.
	Ack(ctx context.Context, tenantID, id, operatorID int64, remark string, at time.Time) error
	// Resolve 解决告警(已确认→已解决)并写审计流水.
	Resolve(ctx context.Context, tenantID, id, operatorID int64, remark string, at time.Time) error
	// CountActive 统计活跃告警总数与按等级分布, 供 M5 大屏 GetActiveAlarms 使用.
	CountActive(ctx context.Context, tenantID, areaID int64, levels []int32) (int64, []LevelCount, error)
}

type alarmModel struct {
	db *gorm.DB
}

// NewAlarmModel 构造基于 GORM 的告警数据访问实现.
func NewAlarmModel(db *gorm.DB) AlarmModel {
	return &alarmModel{db: db}
}

// Create 写入告警并记录一条 action=create 的审计流水(生成动作由系统触发, operator_id 恒为 0).
// 告警与流水在同一事务内, 避免出现"有告警无生成记录"的不一致.
func (m *alarmModel) Create(ctx context.Context, a *Alarm) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(a)
		if res.Error != nil {
			return res.Error
		}
		// MySQL 对冲突行不写入, 影响行数为 0 即 L3 幂等去重命中.
		if res.RowsAffected == 0 {
			return ErrDuplicateRequest
		}
		operateLog := &AlarmOperateLog{
			TenantID:  a.TenantID,
			AlarmID:   a.ID,
			Action:    AlarmActionCreate,
			Remark:    "设备事件命中规则自动生成",
			CreatedAt: a.CreatedAt,
		}
		return tx.Create(operateLog).Error
	})
}

func (m *alarmModel) FindByID(ctx context.Context, tenantID, id int64) (*Alarm, error) {
	var a Alarm
	err := m.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (m *alarmModel) List(ctx context.Context, f AlarmListFilter) ([]*Alarm, int64, error) {
	tx := m.db.WithContext(ctx).Model(&Alarm{}).Where("tenant_id = ?", f.TenantID)
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}
	if f.Level != nil {
		tx = tx.Where("level = ?", *f.Level)
	}
	if f.AreaID != 0 {
		tx = tx.Where("area_id = ?", f.AreaID)
	}
	if f.DeviceID != "" {
		tx = tx.Where("device_id = ?", f.DeviceID)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*Alarm
	err := tx.Order("level DESC, created_at DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (m *alarmModel) Ack(ctx context.Context, tenantID, id, operatorID int64, remark string, at time.Time) error {
	return m.transition(ctx, tenantID, id, AlarmStatusPending, AlarmStatusAcked, AlarmActionAck, map[string]interface{}{
		"ack_by": operatorID,
		"ack_at": at,
	}, operatorID, remark, at)
}

func (m *alarmModel) Resolve(ctx context.Context, tenantID, id, operatorID int64, remark string, at time.Time) error {
	return m.transition(ctx, tenantID, id, AlarmStatusAcked, AlarmStatusResolved, AlarmActionResolve, map[string]interface{}{
		"resolve_by": operatorID,
		"resolve_at": at,
	}, operatorID, remark, at)
}

// transition 在同一事务内完成状态流转与审计流水写入.
// from 为前置状态条件, 不满足则 RowsAffected 为 0 (并发场景下返回 ErrNotFound 由上层转译).
func (m *alarmModel) transition(ctx context.Context, tenantID, id int64,
	from, to int8, action string, fields map[string]interface{}, operatorID int64, remark string, at time.Time) error {
	fields["status"] = to
	fields["updated_at"] = at

	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Alarm{}).
			Where("id = ? AND tenant_id = ? AND status = ?", id, tenantID, from).
			Updates(fields)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		log := &AlarmOperateLog{
			TenantID:   tenantID,
			AlarmID:    id,
			Action:     action,
			OperatorID: operatorID,
			Remark:     remark,
			CreatedAt:  at,
		}
		return tx.Create(log).Error
	})
}

func (m *alarmModel) CountActive(ctx context.Context, tenantID, areaID int64, levels []int32) (int64, []LevelCount, error) {
	tx := m.db.WithContext(ctx).Model(&Alarm{}).
		Where("tenant_id = ? AND status = ?", tenantID, AlarmStatusPending)
	if areaID != 0 {
		tx = tx.Where("area_id = ?", areaID)
	}
	if len(levels) > 0 {
		lv := make([]int8, 0, len(levels))
		for _, l := range levels {
			lv = append(lv, int8(l))
		}
		tx = tx.Where("level IN ?", lv)
	}

	var counts []LevelCount
	if err := tx.Select("level, count(*) AS total").Group("level").Scan(&counts).Error; err != nil {
		return 0, nil, err
	}

	var total int64
	for _, c := range counts {
		total += c.Total
	}
	return total, counts, nil
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
