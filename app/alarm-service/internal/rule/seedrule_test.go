package rule

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// seedSQLPath 建库脚本相对本测试包的位置(app/alarm-service/internal/rule -> 仓库根).
const seedSQLPath = "../../../../deploy/sql/m3_mysql_tables.sql"

// readSeedSQL 读取建库脚本; 单独拷出本模块跑测试时跳过, 不把路径依赖变成失败.
func readSeedSQL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(seedSQLPath)
	if err != nil {
		t.Skipf("建库脚本不可达(单模块跑测试时属正常): %v", err)
	}
	return string(raw)
}

// TestSeedRulesAreParseable 锁定建库脚本里的种子规则条件能被引擎解析.
//
// 为什么值得单测: ListEnabled 对解析失败的规则是**静默跳过**的 ——
// 种子 SQL 写错一个字段名, 现象是"规则明明配了却不告警", 排查成本极高.
// 本用例是 SQL 与规则解析器之间的契约测试, 改任一侧都会在这里失败.
func TestSeedRulesAreParseable(t *testing.T) {
	raw := readSeedSQL(t)

	// 种子条件一律是单引号包裹的 JSON 字面量, 且 JSON 内部不含单引号, 可直接按边界提取.
	re := regexp.MustCompile(`'\{"type":"[^']+'`)
	matches := re.FindAllString(raw, -1)
	if len(matches) < 3 {
		t.Fatalf("种子规则数量异常: 期望 >=3 条, 实际提取到 %d 条(脚本被改动?)", len(matches))
	}

	for _, m := range matches {
		cond := strings.Trim(m, "'")
		spec, err := ParseSpec(cond)
		if err != nil {
			t.Errorf("种子规则条件无法解析: %v\n  raw=%s", err, cond)
			continue
		}
		if !IsValidRuleType(spec.Type) {
			t.Errorf("种子规则类型非法: %q\n  raw=%s", spec.Type, cond)
		}
	}
}

// TestSeedAccessControlIntrusionRuleIsDeviceTypeScoped 门禁闯入规则必须在种子 SQL 里限定设备类型.
//
// 为什么值得单测: 这条规则是安防主链路, 若种子只写 event_type=intrusion 而漏了 device_type,
// 规则会放宽到"任何设备上报 intrusion 都算门禁闯入" —— 现象不是报错, 而是告警变多且看不出原因。
// 本用例把 SQL 里的设备类型与引擎的 AppliesTo 语义绑在一起, 改任一侧都会在这里失败。
func TestSeedAccessControlIntrusionRuleIsDeviceTypeScoped(t *testing.T) {
	raw := readSeedSQL(t)

	// 匹配种子行: (id, tenant_id, name, device_type, device_id, area_id, event_type
	re := regexp.MustCompile(`\(\s*(\d+),\s*0,\s*'[^']*',\s*'([^']*)',\s*'[^']*',\s*0,\s*'([^']*)'`)
	var scoped bool
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		if m[3] != "intrusion" || m[2] == "" {
			continue
		}
		scoped = true
		// SQL 里写的设备类型必须真的能让引擎区分门禁与摄像头, 否则等于没配.
		rule := Rule{DeviceType: m[2], EventType: m[3]}
		if !rule.AppliesTo(Fields{EventType: "intrusion", DeviceType: "access_control"}) {
			t.Errorf("种子规则 id=%s: 门禁设备上报 intrusion 应命中, device_type=%q", m[1], m[2])
		}
		if rule.AppliesTo(Fields{EventType: "intrusion", DeviceType: "camera"}) {
			t.Errorf("种子规则 id=%s: 摄像头上报 intrusion 不应命中, device_type=%q", m[1], m[2])
		}
	}
	if !scoped {
		t.Fatal("种子 SQL 中未找到限定 device_type 的 intrusion 规则(门禁闯入规则被放宽成了全设备?)")
	}
}

// TestParseSpec_FlatFormAcceptsBothOperatorKeys 扁平单条件写法必须同时接受 op 与 operator.
//
// 这条兼容性是种子规则上线时真实踩出来的: 用 {"field":"event_type","op":"eq",...} 的扁平写法时,
// 解析器只读 operator 字段, 操作符变成空串并报「不支持的操作符 ""」——
// 而报错文案里的条件看起来完全合法, 极易被误判成"配置写错了"。
func TestParseSpec_FlatFormAcceptsBothOperatorKeys(t *testing.T) {
	cases := map[string]string{
		"op 写法":       `{"type":"threshold","field":"event_type","op":"eq","value":"intrusion"}`,
		"operator 写法": `{"type":"threshold","field":"event_type","operator":"eq","value":"intrusion"}`,
		"符号写法":        `{"type":"threshold","field":"payload.temperature","operator":">","value":50}`,
	}
	for name, raw := range cases {
		spec, err := ParseSpec(raw)
		if err != nil {
			t.Errorf("%s: 应可解析, 实际 %v", name, err)
			continue
		}
		if len(spec.Conditions) != 1 {
			t.Errorf("%s: 扁平写法应归一为 1 个条件, 实际 %d", name, len(spec.Conditions))
			continue
		}
		// 操作符必须落在条件上, 否则规则会在运行期被 ListEnabled 静默跳过.
		if op := spec.Conditions[0].OperatorOf(); op == "" {
			t.Errorf("%s: 操作符丢失", name)
		}
	}
}

// TestSeedTimeWindowRuleShape 时间窗口种子规则的 window_sec/threshold 必须真的被解析出来.
//
// 只断言"能解析"不够: 若把 window_sec/threshold 写到了 conditions 之外(或写错字段名),
// 引擎会退化成"每条事件都触发" —— 那比规则不生效更危险(告警风暴).
func TestSeedTimeWindowRuleShape(t *testing.T) {
	raw := readSeedSQL(t)

	re := regexp.MustCompile(`'\{"type":"time_window"[^']+'`)
	found := re.FindAllString(raw, -1)
	if len(found) == 0 {
		t.Fatal("未找到时间窗口类型的种子规则")
	}

	for _, m := range found {
		spec, err := ParseSpec(strings.Trim(m, "'"))
		if err != nil {
			t.Fatalf("时间窗口种子规则解析失败: %v", err)
		}
		if spec.Type != RuleTypeTimeWindow {
			t.Errorf("规则类型应为 %s, 实际 %s", RuleTypeTimeWindow, spec.Type)
		}
		if spec.Threshold <= 0 {
			t.Errorf("threshold 必须为正数, 实际 %d", spec.Threshold)
		}
		if spec.WindowSec <= 0 {
			t.Errorf("window_sec 必须为正数, 实际 %d", spec.WindowSec)
		}
		if spec.Match == nil {
			t.Error("时间窗口规则缺少 match 条件")
		}
	}
}
