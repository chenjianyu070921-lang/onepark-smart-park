package model

import (
	"context"
	"errors"
	"time"

	"onepark/app/alarm-service/internal/rule"

	"gorm.io/gorm"
)

// ErrRuleNotFound 规则不存在.
var ErrRuleNotFound = errors.New("alarm: rule not found")

// 规则状态取值.
const (
	RuleStatusDisabled int8 = 0 // 禁用
	RuleStatusEnabled  int8 = 1 // 启用
)

// AlarmRuleListFilter 规则列表筛选条件, 零值字段表示不参与筛选.
type AlarmRuleListFilter struct {
	TenantID   int64
	DeviceType string // 空表示全部设备类型
	DeviceID   string // 空表示全部设备
	EventType  string // 空表示全部事件类型
	Status     *int8  // nil 表示全部状态
	Page       int    // 从 1 开始
	PageSize   int
}

// AlarmRuleModel 告警规则数据访问层, 同时作为规则引擎的规则来源(rule.Store).
type AlarmRuleModel interface {
	// Create 写入规则并返回自增ID.
	Create(ctx context.Context, r *AlarmRule) error
	// Update 按显式字段表更新规则(支持把 status 更新为 0 禁用); RowsAffected 为 0 返回 ErrRuleNotFound.
	// 用 map 而非 struct: GORM 用 struct 更新会跳过零值字段, 导致"禁用规则"写不进去.
	Update(ctx context.Context, tenantID, id int64, updates map[string]interface{}) error
	// FindByID 按租户+主键查询, 不存在返回 ErrRuleNotFound.
	FindByID(ctx context.Context, tenantID, id int64) (*AlarmRule, error)
	// List 分页查询规则, 按 id DESC 排序.
	List(ctx context.Context, f AlarmRuleListFilter) ([]*AlarmRule, int64, error)
	// ListEnabled 供规则引擎加载启用中的规则(实现 rule.Store).
	ListEnabled(ctx context.Context) ([]rule.Rule, error)
}

type alarmRuleModel struct {
	db *gorm.DB
}

// NewAlarmRuleModel 构造基于 GORM 的规则数据访问实现.
func NewAlarmRuleModel(db *gorm.DB) AlarmRuleModel {
	return &alarmRuleModel{db: db}
}

var _ rule.Store = (*alarmRuleModel)(nil)

func (m *alarmRuleModel) Create(ctx context.Context, r *AlarmRule) error {
	return m.db.WithContext(ctx).Create(r).Error
}

func (m *alarmRuleModel) Update(ctx context.Context, tenantID, id int64, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now()
	res := m.db.WithContext(ctx).Model(&AlarmRule{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRuleNotFound
	}
	return nil
}

func (m *alarmRuleModel) FindByID(ctx context.Context, tenantID, id int64) (*AlarmRule, error) {
	var r AlarmRule
	err := m.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&r).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRuleNotFound
		}
		return nil, err
	}
	return &r, nil
}

func (m *alarmRuleModel) List(ctx context.Context, f AlarmRuleListFilter) ([]*AlarmRule, int64, error) {
	tx := m.db.WithContext(ctx).Model(&AlarmRule{}).Where("tenant_id = ?", f.TenantID)
	if f.DeviceType != "" {
		tx = tx.Where("device_type = ?", f.DeviceType)
	}
	if f.DeviceID != "" {
		tx = tx.Where("device_id = ?", f.DeviceID)
	}
	if f.EventType != "" {
		tx = tx.Where("event_type = ?", f.EventType)
	}
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*AlarmRule
	if err := tx.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabled 加载全部启用规则并解析条件 JSON.
// 解析失败的规则会被跳过并降级为"不生效", 避免一条脏数据让整个引擎不可用;
// 上层需靠规则校验(创建/更新时)与监控发现这类脏数据.
//
// 刻意不加 tenant_id 过滤条件: 本表存在平台级规则(tenant_id=0, 见建表脚本 §5 种子规则),
// 它们必须对所有园区的事件生效, 按租户过滤会把平台规则一起滤掉。
// 租户隔离改由引擎在 rule.AppliesTo 里按"规则租户 == 0 或 == 事件租户"判定 ——
// 过滤点与"平台级规则"这条语义放在同一处, 避免两处各判一半。
func (m *alarmRuleModel) ListEnabled(ctx context.Context) ([]rule.Rule, error) {
	var list []*AlarmRule
	if err := m.db.WithContext(ctx).Where("status = ?", RuleStatusEnabled).Find(&list).Error; err != nil {
		return nil, err
	}

	rules := make([]rule.Rule, 0, len(list))
	for _, r := range list {
		spec, err := rule.ParseSpec(r.Conditions)
		if err != nil {
			// 条件非法: 跳过该规则, 不中断其它规则加载.
			continue
		}
		// 库表显式声明的 rule_type 覆盖 JSON 推断结果(见 rule.WithDeclaredType):
		// 控制台改的是列, 引擎若只认 JSON, "改成时间窗口"这类操作会毫无效果.
		spec = rule.WithDeclaredType(spec, r.RuleType)
		rules = append(rules, rule.Rule{
			ID:            r.ID,
			Name:          r.Name,
			TenantID:      r.TenantID,
			RuleType:      r.RuleType,
			WindowSeconds: r.WindowSeconds,
			DeviceType:    r.DeviceType,
			DeviceID:      r.DeviceID,
			AreaID:        r.AreaID,
			EventType:     r.EventType,
			Level:         r.Level,
			Spec:          spec,
		})
	}
	return rules, nil
}
