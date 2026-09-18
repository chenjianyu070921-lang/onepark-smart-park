package rule

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Rule 引擎使用的规则视图(由 model.AlarmRule 转换而来).
type Rule struct {
	ID        int64
	Name      string
	DeviceID  string // 空或 "*" 表示不限设备
	AreaID    int64  // 0 表示不限区域
	EventType string // 空表示不限事件类型
	Level     int8
	Spec      *NormalizedSpec
}

// Store 引擎获取启用规则的来源(由 model 层实现, 便于单测替换为内存实现).
type Store interface {
	// ListEnabled 返回全部启用中的规则.
	ListEnabled(ctx context.Context) ([]Rule, error)
}

// Draft 命中规则后产出的告警草稿: 引擎不落库, 由消费链路负责入库.
type Draft struct {
	RuleID     int64
	RuleName   string
	Level      int8
	Content    string
	EventType  string
	DeviceID   string
	AreaID     int64
	WindowHits int64 // time_window 规则命中时的窗口内计数, 其它类型为 0
}

// Engine 规则引擎: 对单条事件评估全部适用规则, 返回 0..N 条告警草稿(docs/m3/07 §4).
type Engine struct {
	store   Store
	windows WindowCounter // nil 时 time_window 规则将被跳过(不静默降级为无状态规则)
	// cacheTTL 规则快照缓存时间: 规则变更不频繁, 避免每条事件都查库.
	cacheTTL time.Duration

	mu        sync.RWMutex
	cached    []Rule
	cachedAt  time.Time
	cacheLoad bool
}

// NewEngine 构造规则引擎. windows 为 nil 时不支持 time_window 规则(评估时跳过, 不静默退化为逐条触发).
func NewEngine(store Store, windows WindowCounter, cacheTTL time.Duration) *Engine {
	return &Engine{store: store, windows: windows, cacheTTL: cacheTTL}
}

// HasRules 返回当前是否存在启用的规则, 供调用方判断是否回退到硬编码规则.
func (e *Engine) HasRules(ctx context.Context) bool {
	rules, err := e.snapshot(ctx)
	return err == nil && len(rules) > 0
}

// Evaluate 评估事件, 返回命中的告警草稿列表(可能为空).
// 规则本身解析失败只跳过该条规则, 不影响其它规则(docs/m3/07: 单规则异常不应拖垮整条链路).
func (e *Engine) Evaluate(ctx context.Context, f Fields, idempotentID string) ([]*Draft, error) {
	rules, err := e.snapshot(ctx)
	if err != nil {
		return nil, err
	}

	drafts := make([]*Draft, 0, len(rules))
	for _, r := range rules {
		if !r.AppliesTo(f) {
			continue
		}
		if r.Spec == nil {
			continue
		}

		switch r.Spec.Type {
		case RuleTypeThreshold, RuleTypeCombination:
			// 阈值与(单事件内)组合条件复用同一套条件求值, 区别只在条件条数与 logic.
			hit, err := matchConditions(f, r.Spec)
			if err != nil {
				return nil, fmt.Errorf("rule %d(%s) 求值失败: %w", r.ID, r.Name, err)
			}
			if hit {
				drafts = append(drafts, newDraft(r, f, 0))
			}
		case RuleTypeTimeWindow:
			if e.windows == nil {
				// 窗口计数不可用必须显式跳过: 若退化为"每条都触发"会造成告警风暴.
				continue
			}
			if r.Spec.Match != nil {
				ok, err := EvaluateCondition(f, *r.Spec.Match)
				if err != nil {
					return nil, fmt.Errorf("rule %d(%s) match 求值失败: %w", r.ID, r.Name, err)
				}
				if !ok {
					continue
				}
			}
			window := time.Duration(r.Spec.WindowSec) * time.Second
			// 未配置窗口时给默认 60s, 否则 window=0 会让 ZREMRANGEBYSCORE 清掉全部成员.
			if window <= 0 {
				window = 60 * time.Second
			}
			threshold := r.Spec.Threshold
			if threshold <= 0 {
				threshold = 1
			}
			ttl := window + 60*time.Second
			triggered, cnt, err := e.windows.Add(ctx, WindowKey(r.ID, f.DeviceID), idempotentID, window, threshold, ttl)
			if err != nil {
				return nil, fmt.Errorf("rule %d(%s) 窗口计数失败: %w", r.ID, r.Name, err)
			}
			if triggered {
				drafts = append(drafts, newDraft(r, f, cnt))
			}
		}
	}
	return drafts, nil
}

// newDraft 构造告警草稿, 内容按规则类型渲染.
func newDraft(r Rule, f Fields, hits int64) *Draft {
	content := fmt.Sprintf("规则[%s]命中: 设备 %s 上报 %s", r.Name, f.DeviceID, f.EventType)
	if r.Spec != nil && r.Spec.Type == RuleTypeTimeWindow {
		content = fmt.Sprintf("规则[%s]命中: 设备 %s 在窗口内累计 %s 次 %s", r.Name, f.DeviceID,
			strconv.FormatInt(hits, 10), f.EventType)
	}
	return &Draft{
		RuleID:     r.ID,
		RuleName:   r.Name,
		Level:      r.Level,
		Content:    content,
		EventType:  f.EventType,
		DeviceID:   f.DeviceID,
		AreaID:     f.AreaID,
		WindowHits: hits,
	}
}

// AppliesTo 判定规则适用范围: 事件类型/设备/区域三维度, 空值表示该维度不限.
func (r Rule) AppliesTo(f Fields) bool {
	if r.EventType != "" && r.EventType != f.EventType {
		return false
	}
	if r.DeviceID != "" && r.DeviceID != "*" && r.DeviceID != f.DeviceID {
		return false
	}
	if r.AreaID != 0 && r.AreaID != f.AreaID {
		return false
	}
	return true
}

// matchConditions 按 logic 组合多条条件(AND/OR).
func matchConditions(f Fields, spec *NormalizedSpec) (bool, error) {
	if len(spec.Conditions) == 0 {
		// 无条件即"范围匹配即命中", 用于只看 device/event 范围的简易规则.
		return true, nil
	}
	for _, c := range spec.Conditions {
		ok, err := EvaluateCondition(f, c)
		if err != nil {
			return false, err
		}
		if spec.Logic == "OR" && ok {
			return true, nil
		}
		if spec.Logic == "AND" && !ok {
			return false, nil
		}
	}
	return spec.Logic == "AND", nil
}

// snapshot 返回启用规则快照, 带 TTL 缓存; 缓存过期或首次调用时从 Store 加载.
func (e *Engine) snapshot(ctx context.Context) ([]Rule, error) {
	if e.store == nil {
		return nil, nil
	}
	e.mu.RLock()
	// cacheTTL <= 0 表示不缓存(每次查库). 注意不能只比较 time.Since < TTL:
	// Windows 时钟粒度约 15ms, 同一 tick 内 time.Since 可能为 0, 会让 TTL=0 被误判为"未过期".
	if e.cacheLoad && e.cacheTTL > 0 && time.Since(e.cachedAt) < e.cacheTTL {
		defer e.mu.RUnlock()
		return e.cached, nil
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// 双重检查: 并发下可能已有协程刚刷新过缓存.
	if e.cacheLoad && e.cacheTTL > 0 && time.Since(e.cachedAt) < e.cacheTTL {
		return e.cached, nil
	}
	rules, err := e.store.ListEnabled(ctx)
	if err != nil {
		// 加载失败保留旧快照(存储恢复后无需重新预热), 但错误必须上抛:
		// 调用方据此重试, 不能拿过期规则继续评估导致误报.
		return e.cached, err
	}
	e.cached = rules
	e.cachedAt = time.Now()
	e.cacheLoad = true
	return e.cached, nil
}
