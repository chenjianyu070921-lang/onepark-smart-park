// Package rule 实现告警规则引擎(docs/m3/07): 阈值 threshold / 组合 combination / 时间窗口 time_window.
// 边界(见 docs/m3/07 §1): 只负责"事件 → 告警草稿"的判定, 不做入库 / ES 双写 / 推送, 也不负责规则 CRUD.
package rule

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 规则类型取值. docs/m3/04 #34 写的是 composite/window, docs/m3/07 写的是 combination/time_window,
// 两份文档不一致, 故以 07(引擎设计)为准, 并对 04 的写法做别名兼容.
const (
	RuleTypeThreshold   = "threshold"
	RuleTypeCombination = "combination"
	RuleTypeTimeWindow  = "time_window"
)

// ruleTypeAlias 文档 04 与 07 的命名差异映射.
var ruleTypeAlias = map[string]string{
	"composite": RuleTypeCombination,
	"window":    RuleTypeTimeWindow,
}

// Condition 单个判定条件. docs/m3/07 用 op, docs/m3/04 #34 的示例用 operator, 两者都接受.
type Condition struct {
	Field    string      `json:"field"`
	Op       string      `json:"op"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

// Op 返回归一化后的操作符(兼容 operator 字段).
func (c Condition) OperatorOf() string {
	if c.Op != "" {
		return c.Op
	}
	return c.Operator
}

// Spec 规则条件 JSON 的统一结构(docs/m3/07 §2).
// Threshold 仅 time_window 使用; Match 仅 time_window 使用.
type Spec struct {
	Type       string      `json:"type"`
	Logic      string      `json:"logic"`
	Conditions []Condition `json:"conditions"`
	Field      string      `json:"field"`
	Operator   string      `json:"operator"`
	Value      interface{} `json:"value"`
	WindowSec  int         `json:"window_sec"`
	Threshold  int         `json:"threshold"`
	Match      *Condition  `json:"match"`
}

// NormalizedSpec 解析并归一化后的规则条件, 供求值器直接使用.
type NormalizedSpec struct {
	Type       string
	Logic      string // AND / OR, 默认 AND
	Conditions []Condition
	WindowSec  int
	Threshold  int
	Match      *Condition
}

// ParseSpec 解析规则条件 JSON, 并归一化类型名与条件列表.
// 兼容两种写法: 07 的嵌套 conditions 数组, 以及 04 的单条件扁平形式(如文档中的 temperature>50).
func ParseSpec(raw string) (*NormalizedSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("rule: conditions 为空")
	}
	var spec Spec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("rule: conditions JSON 解析失败: %w", err)
	}

	ruleType := strings.ToLower(strings.TrimSpace(spec.Type))
	if ruleType == "" {
		// 未显式声明 type 时, 依据字段推断: 有 threshold/窗口 → 时间窗口, 否则按阈值处理.
		if spec.Threshold > 0 || spec.WindowSec > 0 && spec.Match != nil {
			ruleType = RuleTypeTimeWindow
		} else {
			ruleType = RuleTypeThreshold
		}
	}
	if alias, ok := ruleTypeAlias[ruleType]; ok {
		ruleType = alias
	}

	conditions := spec.Conditions
	if len(conditions) == 0 && spec.Field != "" {
		// 扁平单条件写法(doc 04): {"field":"temperature","operator":">","value":50}
		conditions = []Condition{{Field: spec.Field, Op: spec.Operator, Value: spec.Value}}
	}

	logic := strings.ToUpper(strings.TrimSpace(spec.Logic))
	if logic == "" {
		logic = "AND"
	}
	if logic != "AND" && logic != "OR" {
		return nil, fmt.Errorf("rule: logic 仅支持 AND/OR, 实际 %s", spec.Logic)
	}

	// 条件校验必须在解析期完成, 避免运行期每条事件才发现配置错误.
	for i, c := range conditions {
		if strings.TrimSpace(c.Field) == "" {
			return nil, fmt.Errorf("rule: 第 %d 个条件缺少 field", i+1)
		}
		if _, err := toOperator(c.OperatorOf()); err != nil {
			return nil, fmt.Errorf("rule: 第 %d 个条件操作符非法: %w", i+1, err)
		}
	}
	if ruleType == RuleTypeTimeWindow && spec.Match != nil {
		if _, err := toOperator(spec.Match.OperatorOf()); err != nil {
			return nil, fmt.Errorf("rule: match 条件操作符非法: %w", err)
		}
	}

	return &NormalizedSpec{
		Type:       ruleType,
		Logic:      logic,
		Conditions: conditions,
		WindowSec:  spec.WindowSec,
		Threshold:  spec.Threshold,
		Match:      spec.Match,
	}, nil
}

// IsValidRuleType 判定规则类型是否为引擎支持的三种(含别名).
func IsValidRuleType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case RuleTypeThreshold, RuleTypeCombination, RuleTypeTimeWindow:
		return true
	}
	_, ok := ruleTypeAlias[strings.ToLower(strings.TrimSpace(t))]
	return ok
}

// operator 归一化后的比较算子.
type operator string

const (
	opGT      operator = "gt"
	opGTE     operator = "gte"
	opLT      operator = "lt"
	opLTE     operator = "lte"
	opEQ      operator = "eq"
	opNE      operator = "ne"
	opIn      operator = "in"
	opBetween operator = "between"
)

// opAlias 兼容文档 04 里出现的符号写法(如 ">" ">=" "=").
var opAlias = map[string]operator{
	">": opGT, ">=": opGTE, "<": opLT, "<=": opLTE, "=": opEQ, "==": opEQ, "!=": opNE, "<>": opNE,
}

// toOperator 归一化操作符字符串, 非法操作符直接报错.
func toOperator(raw string) (operator, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if o, ok := opAlias[raw]; ok {
		return o, nil
	}
	switch operator(normalized) {
	case opGT, opGTE, opLT, opLTE, opEQ, opNE, opIn, opBetween:
		return operator(normalized), nil
	default:
		return "", fmt.Errorf("不支持的操作符 %q", raw)
	}
}

// Fields 事件可被引擎取值的字段集合(docs/m3/07 §2: payload.xxx / event_type / device_id ...).
type Fields struct {
	EventType  string
	DeviceID   string
	DeviceType string
	AreaID     int64
	TenantID   int64
	Payload    map[string]interface{}
}

// ResolveField 按路径取值: 支持 payload.xxx(可多级)、event_type、device_id、device_type、area_id、tenant_id.
func (f Fields) ResolveField(path string) (interface{}, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	switch path {
	case "event_type":
		return f.EventType, true
	case "device_id":
		return f.DeviceID, true
	case "device_type":
		return f.DeviceType, true
	case "area_id":
		return f.AreaID, true
	case "tenant_id":
		return f.TenantID, true
	}
	segments := strings.Split(path, ".")
	if segments[0] != "payload" {
		return nil, false
	}
	var cur interface{} = f.Payload
	for _, seg := range segments[1:] {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// EvaluateCondition 对单条条件求值.
func EvaluateCondition(f Fields, c Condition) (bool, error) {
	op, err := toOperator(c.OperatorOf())
	if err != nil {
		return false, err
	}
	actual, ok := f.ResolveField(c.Field)
	if !ok {
		// 字段缺失一律视为未命中: 设备上报字段不齐是常态, 不能因此判定为满足阈值.
		return false, nil
	}
	return compare(op, actual, c.Value)
}

// compare 按算子比较实际值与期望值.
func compare(op operator, actual, expected interface{}) (bool, error) {
	switch op {
	case opBetween:
		// between 的期望值为 [下界, 上界] 两元组.
		bounds, ok := expected.([]interface{})
		if !ok || len(bounds) != 2 {
			return false, fmt.Errorf("between 需要长度为 2 的数组作为 value")
		}
		lo, err := toFloat(bounds[0])
		if err != nil {
			return false, err
		}
		hi, err := toFloat(bounds[1])
		if err != nil {
			return false, err
		}
		v, err := toFloat(actual)
		if err != nil {
			return false, nil
		}
		return v >= lo && v <= hi, nil
	case opIn:
		members, ok := expected.([]interface{})
		if !ok {
			// 也允许 "a,b,c" 逗号分隔写法.
			if s, isStr := expected.(string); isStr {
				members = make([]interface{}, 0)
				for _, item := range strings.Split(s, ",") {
					members = append(members, strings.TrimSpace(item))
				}
			} else {
				return false, fmt.Errorf("in 需要数组或逗号分隔字符串作为 value")
			}
		}
		for _, m := range members {
			if equalValue(actual, m) {
				return true, nil
			}
		}
		return false, nil
	}

	// 数值比较优先按 float64 走; 双方都非数字时退化为字符串比较.
	actualNum, actualErr := toFloat(actual)
	expectNum, expectErr := toFloat(expected)
	if actualErr == nil && expectErr == nil {
		switch op {
		case opGT:
			return actualNum > expectNum, nil
		case opGTE:
			return actualNum >= expectNum, nil
		case opLT:
			return actualNum < expectNum, nil
		case opLTE:
			return actualNum <= expectNum, nil
		case opEQ:
			return actualNum == expectNum, nil
		case opNE:
			return actualNum != expectNum, nil
		}
	}
	a, e := fmt.Sprint(actual), fmt.Sprint(expected)
	switch op {
	case opGT:
		return a > e, nil
	case opGTE:
		return a >= e, nil
	case opLT:
		return a < e, nil
	case opLTE:
		return a <= e, nil
	case opEQ:
		return a == e, nil
	case opNE:
		return a != e, nil
	}
	return false, fmt.Errorf("未处理的算子 %s", op)
}

// equalValue 判定相等(数值/字符串两种形态都尝试).
func equalValue(actual, expected interface{}) bool {
	if a, err := toFloat(actual); err == nil {
		if e, err := toFloat(expected); err == nil {
			return a == e
		}
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected)
}

// toFloat 将 JSON 反序列化后的数值/字符串统一转为 float64.
func toFloat(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case json.Number:
		f, err := n.Float64()
		return f, err
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, fmt.Errorf("空字符串无法转为数值")
		}
		return strconv.ParseFloat(s, 64)
	default:
		return 0, fmt.Errorf("类型 %T 无法转为数值", v)
	}
}
