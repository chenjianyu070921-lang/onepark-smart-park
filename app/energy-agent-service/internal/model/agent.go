package model

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 运行状态
const (
	RunStatusRunning = 1
	RunStatusSuccess = 2
	RunStatusFailed  = 3
)

// 建议状态
const (
	SugStatusPending   = 1 // 待审批
	SugStatusApproved  = 2 // 已通过
	SugStatusRejected  = 3 // 已驳回
	SugStatusConverted = 4 // 已转工单
)

// AgentRun 一次巡检的运行记录
type AgentRun struct {
	ID          uint64     `gorm:"primaryKey;column:id"`
	RunNo       string     `gorm:"column:run_no"`
	TriggerType string     `gorm:"column:trigger_type"`
	Scope       string     `gorm:"column:scope"`
	StatDate    string     `gorm:"column:stat_date"`
	Status      int        `gorm:"column:status"`
	ZoneCount   int        `gorm:"column:zone_count"`
	FindCount   int        `gorm:"column:find_count"`
	LLMEnabled  int        `gorm:"column:llm_enabled"`
	LLMModel    string     `gorm:"column:llm_model"`
	TokensUsed  int        `gorm:"column:tokens_used"`
	CostMs      int        `gorm:"column:cost_ms"`
	ErrorMsg    string     `gorm:"column:error_msg"`
	StartedAt   time.Time  `gorm:"column:started_at"`
	FinishedAt  *time.Time `gorm:"column:finished_at"`
}

func (AgentRun) TableName() string { return "agent_run" }

// AgentToolCall 工具调用轨迹
type AgentToolCall struct {
	ID         uint64    `gorm:"primaryKey;column:id"`
	RunID      uint64    `gorm:"column:run_id"`
	ToolName   string    `gorm:"column:tool_name"`
	Params     string    `gorm:"column:params;type:json"`
	ResultSum  string    `gorm:"column:result_sum"`
	DurationMs int       `gorm:"column:duration_ms"`
	Success    int       `gorm:"column:success"`
	ErrorMsg   string    `gorm:"column:error_msg"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (AgentToolCall) TableName() string { return "agent_tool_call" }

// AgentSuggestion 优化建议
type AgentSuggestion struct {
	ID          uint64     `gorm:"primaryKey;column:id"`
	RunID       uint64     `gorm:"column:run_id"`
	StatDate    time.Time  `gorm:"column:stat_date;type:date"` // 统计日, 参与去重
	ZoneID      string     `gorm:"column:zone_id"`
	DeviceID    string     `gorm:"column:device_id"`
	Category    string     `gorm:"column:category"`
	Severity    int        `gorm:"column:severity"`
	Title       string     `gorm:"column:title"`
	Reason      string     `gorm:"column:reason"`
	Evidence    string     `gorm:"column:evidence;type:json"`
	Confidence  float64    `gorm:"column:confidence"`
	Action      string     `gorm:"column:action"`
	Status      int        `gorm:"column:status"`
	Reviewer    string     `gorm:"column:reviewer"`
	ReviewedAt  *time.Time `gorm:"column:reviewed_at"`
	WorkorderNo string     `gorm:"column:workorder_no"`
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

func (AgentSuggestion) TableName() string { return "agent_suggestion" }

// SuggestionFilter 列表筛选条件
type SuggestionFilter struct {
	ZoneID   string
	Category string
	Status   int // 0 表示不限
	Page     int
	PageSize int
}

// AgentModel 智能体三张表的读写
type AgentModel struct {
	db *gorm.DB
}

func NewAgentModel(db *gorm.DB) *AgentModel {
	return &AgentModel{db: db}
}

// CreateRun 开一次巡检, 返回主键
func (m *AgentModel) CreateRun(ctx context.Context, r *AgentRun) error {
	return m.db.WithContext(ctx).Create(r).Error
}

// FinishRun 结束一次巡检。
//
// llmEnabled / llmModel 必须在这里由调用方传进来写库, 不能靠改内存里的结构体:
// 调用方拿到的 run 是 Create 之后的一个副本, 改它的字段不会同步到数据库,
// 结果表里 llm_enabled 永远是 0 —— 事后就没法判断"这次到底用没用大模型"。
func (m *AgentModel) FinishRun(ctx context.Context, id uint64,
	status, zoneCount, findCount int, llmEnabled bool, llmModel string, costMs int, errMsg string) error {
	now := time.Now()
	llm := 0
	if llmEnabled {
		llm = 1
	}
	return m.db.WithContext(ctx).Model(&AgentRun{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"zone_count":  zoneCount,
			"find_count":  findCount,
			"llm_enabled": llm,
			"llm_model":   llmModel,
			"tokens_used": 0,
			"cost_ms":     costMs,
			"error_msg":   errMsg,
			"finished_at": now,
		}).Error
}

// AddToolCall 记一条工具调用
func (m *AgentModel) AddToolCall(ctx context.Context, c *AgentToolCall) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	return m.db.WithContext(ctx).Create(c).Error
}

// SaveSuggestions 批量保存建议, 带证据序列化。
//
// 用 ON DUPLICATE KEY UPDATE 去重: 同一天 + 同一区域设备 + 同一类问题只留一条。
// 没有这个, 定时任务每天跑一遍、演示时又手动点几次, 列表里会堆满一模一样的重复项,
// 运维翻两页就不看了 —— 那种"智能"反而没人用。
//
// 注意更新时不动 status / reviewer / reviewed_at:
// 已经审批过的建议(通过或驳回)不该因为一次新的巡检又变回"待审批",
// 运维做过的决定要保留下来。
func (m *AgentModel) SaveSuggestions(ctx context.Context, list []AgentSuggestion) error {
	if len(list) == 0 {
		return nil
	}
	return m.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "stat_date"}, {Name: "zone_id"}, {Name: "device_id"}, {Name: "category"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"run_id", "severity", "title", "reason", "evidence", "confidence", "action", "updated_at",
			}),
		}).
		CreateInBatches(list, 50).Error
}

// ListSuggestions 分页查建议
func (m *AgentModel) ListSuggestions(ctx context.Context, f SuggestionFilter) ([]AgentSuggestion, int64, error) {
	q := m.db.WithContext(ctx).Model(&AgentSuggestion{})
	if f.ZoneID != "" {
		q = q.Where("zone_id = ?", f.ZoneID)
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	if f.Status > 0 {
		q = q.Where("status = ?", f.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}

	var list []AgentSuggestion
	err := q.Order("severity DESC, confidence DESC, id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error
	return list, total, err
}

// GetSuggestion 查单条建议
func (m *AgentModel) GetSuggestion(ctx context.Context, id uint64) (*AgentSuggestion, error) {
	var s AgentSuggestion
	err := m.db.WithContext(ctx).Where("id = ?", id).Take(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ApproveSuggestion 审批: 通过或驳回
func (m *AgentModel) ApproveSuggestion(ctx context.Context, id uint64, approved bool, reviewer string) error {
	status := SugStatusRejected
	if approved {
		status = SugStatusApproved
	}
	now := time.Now()
	return m.db.WithContext(ctx).Model(&AgentSuggestion{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"reviewer":    reviewer,
			"reviewed_at": now,
		}).Error
}

// EncodeEvidence 把证据 map 序列化成 JSON 字符串。
// 没有证据的时候必须返回 "{}" 而不是空串 —— MySQL 的 JSON 列不接受空字符串,
// 会报 "Invalid JSON text: The document is empty", 整条建议就存不进去了。
// 序列化失败也一样: 宁可存个空对象, 也不能把空串塞进 JSON 列。
func EncodeEvidence(ev map[string]float64) string {
	if len(ev) == 0 {
		return "{}"
	}
	b, err := json.Marshal(ev)
	if err != nil || len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// DecodeEvidence 反序列化证据
func DecodeEvidence(s string) map[string]float64 {
	if s == "" {
		return nil
	}
	out := make(map[string]float64)
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
