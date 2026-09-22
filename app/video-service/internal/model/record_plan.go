package model

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 录像计划错误.
var (
	// ErrRecordPlanNotFound 计划不存在(或不属于当前租户).
	ErrRecordPlanNotFound = errors.New("video: record plan not found")
	// ErrRecordPlanDuplicate 同一摄像头下计划名称重复(uk_camera_name 命中).
	ErrRecordPlanDuplicate = errors.New("video: record plan duplicated")
)

// 录像策略取值(record_plan.strategy).
const (
	RecordStrategyAlways    = "always"    // 全天录像
	RecordStrategyScheduled = "scheduled" // 按天+时段录像
)

// 录像计划状态(record_plan.status).
const (
	RecordPlanStatusDisabled int8 = 0 // 停用: 不产生录像窗口, 历史计划应停用而非删除
	RecordPlanStatusEnabled  int8 = 1 // 启用
)

// 一天的分钟数, 用于 scheduled 策略的时段边界.
const minutesPerDay = 1440

// RecordPlan 录像计划表(video_db.record_plan).
//
// 计划本身不存媒体: M3 只描述"应当录像的时间", 实际录像由流媒体网关承载
// (docs/m3/11)。回放查询据此推导可用窗口 —— 计划没覆盖的时段不会凭空返回录像。
type RecordPlan struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TenantID   int64  `gorm:"column:tenant_id;not null;index:idx_tenant_status" json:"tenant_id"`
	CameraID   int64  `gorm:"column:camera_id;not null;index:idx_camera" json:"camera_id"`
	Name       string `gorm:"column:name;type:varchar(64);not null;default:''" json:"name"`
	Strategy   string `gorm:"column:strategy;type:varchar(16);not null;default:always" json:"strategy"`
	DaysOfWeek string `gorm:"column:days_of_week;type:varchar(32);not null;default:''" json:"days_of_week"`
	// StartMinute/EndMinute 为当日起始/结束分钟数, 仅 scheduled 策略有效;
	// 不支持跨零点(EndMinute <= StartMinute), 需要跨零点请拆成两条计划.
	StartMinute   int       `gorm:"column:start_minute;not null;default:0" json:"start_minute"`
	EndMinute     int       `gorm:"column:end_minute;not null;default:1440" json:"end_minute"`
	RetentionDays int       `gorm:"column:retention_days;not null;default:7" json:"retention_days"`
	Status        int8      `gorm:"column:status;not null" json:"status"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

// TableName 指定录像计划表名.
func (RecordPlan) TableName() string { return "record_plan" }

// RecordPlanListFilter 录像计划列表筛选条件, 零值字段表示不参与筛选.
type RecordPlanListFilter struct {
	TenantID int64
	CameraID int64 // 0 表示全部摄像头
	Status   *int8 // nil 表示全部状态
	Page     int   // 从 1 开始
	PageSize int
}

// RecordPlanPatch 录像计划的可变字段增量更新, 指针为 nil 表示"不修改该字段".
// 与 CameraPatch 同一定制: status=0(停用) 是合法取值, 不能用零值判断"是否传入".
type RecordPlanPatch struct {
	Name          *string
	Strategy      *string
	DaysOfWeek    *string
	StartMinute   *int
	EndMinute     *int
	RetentionDays *int
	Status        *int8
}

// RecordPlanModel 录像计划数据访问层, 接口化以便单测替换.
type RecordPlanModel interface {
	// Create 写入计划; 同摄像头重名返回 ErrRecordPlanDuplicate.
	Create(ctx context.Context, p *RecordPlan) error
	// FindByID 按租户+主键查询, 不存在返回 ErrRecordPlanNotFound.
	FindByID(ctx context.Context, tenantID, id int64) (*RecordPlan, error)
	// Update 按租户+主键增量更新, 不存在返回 ErrRecordPlanNotFound.
	Update(ctx context.Context, tenantID, id int64, patch RecordPlanPatch) error
	// Delete 按租户+主键删除计划.
	Delete(ctx context.Context, tenantID, id int64) error
	// List 分页查询, 按 id DESC 排序.
	List(ctx context.Context, f RecordPlanListFilter) ([]*RecordPlan, int64, error)
	// ListEnabledByCamera 查询某摄像头的全部启用计划(回放窗口推导的唯一数据来源),
	// 无计划时返回空切片而非错误 —— "没配计划"是合法状态, 不等于查询失败.
	ListEnabledByCamera(ctx context.Context, tenantID, cameraID int64) ([]*RecordPlan, error)
}

// NormalizeMinutes 把 [startMinute, endMinute) 归一到合法范围.
//
// 归一而不是报错: 始末分钟属于"配置友好度"参数 —— 用户填 1500 的意图显然是当天到最后一刻,
// 直接拒绝会让配置过程变成猜错就重来的往返; 真正的硬约束(开始必须早于结束)由调用方判定,
// 因为把这类"自相矛盾"的配置静默修好, 反而会让"到底录不录"说不清.
func NormalizeMinutes(start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > minutesPerDay {
		start = minutesPerDay
	}
	if end < 0 {
		end = 0
	}
	if end > minutesPerDay {
		end = minutesPerDay
	}
	return start, end
}

// ParseDaysOfWeek 解析 "1,2,3" 形式的生效日: 1=周一 ... 7=周日.
//
// 空串返回空集合, 语义是"每天生效"而不是"没有一天生效" —— 后者会让一条刚配好的定时计划
// 一段录像都不产生, 且列表查询返回的结果看起来完全正常, 是很难定位的一类静默失效.
func ParseDaysOfWeek(s string) (map[time.Weekday]bool, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return map[time.Weekday]bool{}, nil
	}
	out := map[time.Weekday]bool{}
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 7 {
			return nil, errors.New("days_of_week 每项需为 1(周一)~7(周日)")
		}
		// time.Weekday: 0=周日, 1=周一 ... 6=周六; 输入 7(周日) 取模映射到 0.
		out[time.Weekday(n%7)] = true
	}
	return out, nil
}

// FormatDaysOfWeek 归一为升序去重字符串, 使同一语义总是落库成同一份文本,
// 避免 "1,2" 与 "2,1" 在列表里看起来像两条不同的配置.
func FormatDaysOfWeek(days map[time.Weekday]bool) string {
	if len(days) == 0 {
		return ""
	}
	set := map[int]bool{}
	for w := range days {
		// 反向映射: 0(周日) 显示为 7, 其余为 w 本身(1=周一 ... 6=周六).
		n := int(w)
		if n == 0 {
			n = 7
		}
		set[n] = true
	}
	list := make([]int, 0, len(set))
	for n := range set {
		list = append(list, n)
	}
	sort.Ints(list)
	parts := make([]string, 0, len(list))
	for _, n := range list {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}
