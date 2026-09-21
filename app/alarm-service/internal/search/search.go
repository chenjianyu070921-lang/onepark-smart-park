// Package search 承载 M3 历史告警检索(#41)与告警双写(#44)的存储访问层.
//
// 设计要点(docs/m3/04 §7):
//   - 索引 alarm_history 只做"检索副本", MySQL 仍是唯一事实来源;
//     因此 ES 不可用时调用方降级查 MySQL, 而不是把接口打挂(降级必须记日志).
//   - 抽象成 Searcher 接口: logic 单测可用替身覆盖"ES 命中 / ES 故障降级"两条路径,
//     无需真实 ES 集群.
package search

import (
	"context"
	"time"
)

// DefaultIndex 历史告警索引名(docs/m3/04 §7.1).
const DefaultIndex = "alarm_history"

// Doc 写入/读出 ES 的告警文档, 字段与索引 mapping 一一对应.
// TenantID 必须写入: 检索时按租户下推过滤, 否则跨园区泄漏(与 MySQL 侧 tenant_id 隔离同义).
type Doc struct {
	AlarmID    int64     `json:"alarm_id"`
	TenantID   int64     `json:"tenant_id"`
	AlarmNo    string    `json:"alarm_no"`
	DeviceID   string    `json:"device_id"`
	AreaID     int64     `json:"area_id"`
	EventType  string    `json:"event_type"`
	Level      int8      `json:"level"`
	Status     int8      `json:"status"`
	Content    string    `json:"content"`
	CreateTime time.Time `json:"create_time"`
}

// Query 历史告警检索条件.
// 指针字段为 nil 表示不参与过滤(level=0/status=0 都是有效取值, 不能用零值判断"是否传入").
type Query struct {
	TenantID  int64
	StartTime time.Time // 零值表示不限起始时间
	EndTime   time.Time // 零值表示不限结束时间
	Level     *int8
	Status    *int8
	AreaID    int64
	DeviceID  string
	EventType string
	// Keyword 告警内容关键词全文检索; 空表示不参与检索(与新增该字段前行为一致).
	// 走 bool.filter 里的 match: 排序固定为 等级降序 + 时间降序, 不按相关性打分,
	// 因此 match 放 filter 上下文即可, 不影响既有排序口径.
	Keyword  string
	Page     int // 从 1 开始
	PageSize int
}

// LevelCount 按告警等级聚合的计数(与 model.LevelCount 同构, 但本包不反向依赖 model).
type LevelCount struct {
	Level int8
	Total int64
}

// Result 检索结果: 当前页文档 + 命中总数 + 等级分布聚合.
type Result struct {
	Docs        []Doc
	Total       int64
	LevelCounts []LevelCount
}

// Searcher 历史告警的写入与检索抽象.
type Searcher interface {
	// Index 按 alarm_id 幂等写入一条告警文档(重复写入覆盖同一条, 天然支持消息重放).
	Index(ctx context.Context, doc Doc) error
	// Search 组合条件检索当前页文档 + 命中总数 + 等级聚合.
	Search(ctx context.Context, q Query) (*Result, error)
}
