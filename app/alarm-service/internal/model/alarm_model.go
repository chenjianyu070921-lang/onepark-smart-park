package model

import (
	"context"
	"errors"
	"strings"
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

// AlarmHistoryFilter 历史告警检索条件(docs/m3/04 #41).
// 指针字段为 nil 表示不参与过滤; 该条件用于 ES 不可用时的 MySQL 降级查询,
// 过滤口径必须与 ES 侧(search.Query)保持一致, 否则降级会返回不同结果.
type AlarmHistoryFilter struct {
	TenantID  int64
	StartTime *time.Time // nil 表示不限起始时间
	EndTime   *time.Time // nil 表示不限结束时间
	Level     *int8
	Status    *int8
	AreaID    int64
	DeviceID  string
	EventType string
	// Keyword 告警内容关键词; 空表示不参与检索.
	// 与 ES 侧的语义差异是有意为之: MySQL 降级路径只能做子串匹配(LIKE '%kw%'),
	// 而 ES 走分词后的 match —— 同一关键词在两条路径上召回范围可能不同(ES 更宽)。
	// 之所以不"ES 不可用就忽略关键词": 忽略会静默返回未过滤的全量,
	// 用户以为搜过了, 实际什么都没过滤 —— 比召回略窄更难发现。
	Keyword  string
	Page     int // 从 1 开始
	PageSize int
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
	// tenantID/areaID 为 0 表示不参与过滤, levels 为空表示全部等级.
	CountActive(ctx context.Context, tenantID, areaID int64, levels []int32) (int64, []LevelCount, error)
	// SearchHistory 历史告警多条件检索(#41) + 等级分布聚合.
	// 这是 ES 检索不可用时的降级路径: MySQL 缺少全文检索能力, 但条件过滤与聚合口径必须一致.
	SearchHistory(ctx context.Context, f AlarmHistoryFilter) ([]*Alarm, int64, []LevelCount, error)
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

// CountActive 统计活跃告警总数与按等级分布.
// tenantID 为 0 表示**不过滤租户**(跨园区聚合), 与 proto GetActiveAlarmsReq.tenant_id 契约一致;
// M5 大屏的 AlarmProvider.Stat(ctx) 无租户入参, 传 0 即依赖此语义, 否则大屏恒显示 0 条.
func (m *alarmModel) CountActive(ctx context.Context, tenantID, areaID int64, levels []int32) (int64, []LevelCount, error) {
	tx := m.db.WithContext(ctx).Model(&Alarm{}).Where("status = ?", AlarmStatusPending)
	if tenantID != 0 {
		tx = tx.Where("tenant_id = ?", tenantID)
	}
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

// SearchHistory 历史告警检索 + 等级聚合(ES 降级路径).
// 两次查询(分页列表 / 等级聚合)共用同一份过滤条件构造, 避免两处 where 漂移导致
// "列表 10 条但聚合 8 条"这类难以排查的对不上的结果.
func (m *alarmModel) SearchHistory(ctx context.Context, f AlarmHistoryFilter) ([]*Alarm, int64, []LevelCount, error) {
	var total int64
	if err := historyScope(m.db.WithContext(ctx).Model(&Alarm{}), f).Count(&total).Error; err != nil {
		return nil, 0, nil, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*Alarm
	if err := historyScope(m.db.WithContext(ctx).Model(&Alarm{}), f).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error; err != nil {
		return nil, 0, nil, err
	}

	var counts []LevelCount
	// 聚合只扫 level 列: 用 Select + Group 让 MySQL 走索引扫描, 不把整行数据取回应用层.
	if err := historyScope(m.db.WithContext(ctx).Model(&Alarm{}), f).
		Select("level, count(*) AS total").Group("level").Order("level DESC").
		Scan(&counts).Error; err != nil {
		return nil, 0, nil, err
	}
	return list, total, counts, nil
}

// historyScope 构造历史检索的过滤条件(列表与聚合共用).
func historyScope(tx *gorm.DB, f AlarmHistoryFilter) *gorm.DB {
	tx = tx.Where("tenant_id = ?", f.TenantID)
	if f.StartTime != nil {
		tx = tx.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		tx = tx.Where("created_at <= ?", *f.EndTime)
	}
	if f.Level != nil {
		tx = tx.Where("level = ?", *f.Level)
	}
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}
	if f.AreaID != 0 {
		tx = tx.Where("area_id = ?", f.AreaID)
	}
	if f.DeviceID != "" {
		tx = tx.Where("device_id = ?", f.DeviceID)
	}
	if f.EventType != "" {
		tx = tx.Where("event_type = ?", f.EventType)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		// 全模糊匹配无法走索引, 仅用于 ES 不可用时的降级路径;
		// 通配符与 % 需转义, 否则用户输入的 % 会变成"匹配一切"。
		tx = tx.Where("content LIKE ? ESCAPE '\\'", "%"+escapeLike(kw)+"%")
	}
	return tx
}

// escapeLike 转义 LIKE 的通配符与转义符本身, 防止用户输入的 %/_ 改变匹配语义.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
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
