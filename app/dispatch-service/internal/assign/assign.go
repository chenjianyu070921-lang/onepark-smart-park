// Package assign 实现调度工单的指派策略.
//
// 打分模型(加权求和, 权重设计保证**字典序**: 技能 > 距离 > 负载):
//
//	score = 10000 * 技能匹配 + 100 * (3 - 距离) + (99 - min(负载, 99))
//
// 权重为什么这么取:
//   - 技能是"能不能干"的问题, 必须压倒性优先 —— 派错人等于没派;
//   - 负载项被截断在 99 以内, 保证再大的负载差也翻不过"距离"一档(100),
//     距离差也翻不过"技能"一档(10000)。三层优先级不会互相污染。
//
// 「就近」用区域编码的拓扑距离代替经纬度: 园区场景 zone_code 形如 A-3F-301,
// 逐段比较即可表达"同房间 / 同楼层 / 同楼栋 / 跨楼栋", 零依赖且可单测。
package assign

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
)

// 打分权重: 见包注释, 三者需保持 10000 >> 100 >> 99 的量级差.
const (
	skillWeight = 10000
	zoneWeight  = 100
	loadCap     = 99
	maxZoneDist = 3
)

// Candidate 候选处理人及其当前负载与技能.
type Candidate struct {
	AssigneeId   int64
	AssigneeName string
	ZoneCode     string   // 常驻/最近被指派的区域, 用于就近比较
	Skills       []string // 技能标签(已小写)
	Load         int64    // 当前在手工单数
}

// ScoreBreakdown 一次打分的明细, 用于日志留痕: 派单决策必须可解释.
type ScoreBreakdown struct {
	SkillMatched bool   // 是否命中所需技能(未指定技能时恒为 true)
	Distance     int    // 拓扑距离 0~3
	Load         int64  // 当前在手工单数
	Score        int    // 加权总分
}

// ZoneDistance 计算两个区域编码的拓扑距离:
//
//	0 = 同一区域/房间; 1 = 同楼层不同房间; 2 = 同楼栋不同楼层; 3 = 跨楼栋(或编码缺失)
func ZoneDistance(a, b string) int {
	if a == "" || b == "" {
		return 3
	}
	if a == b {
		return 0
	}
	if floorKey(a) == floorKey(b) {
		return 1
	}
	if buildingKey(a) == buildingKey(b) {
		return 2
	}
	return 3
}

// floorKey 取"楼栋-楼层"两段作为楼层标识, 如 A-3F-301 -> A-3F.
func floorKey(s string) string {
	p := parts(s)
	if len(p) >= 2 {
		return p[0] + "-" + p[1]
	}
	return p[0]
}

// buildingKey 取楼栋段, 如 A-3F-301 -> A.
func buildingKey(s string) string {
	return parts(s)[0]
}

func parts(s string) []string {
	return strings.Split(strings.Trim(s, "-/"), "-")
}

// HasSkill 判断候选是否具备某技能(大小写不敏感).
func (c Candidate) HasSkill(skill string) bool {
	want := strings.ToLower(strings.TrimSpace(skill))
	if want == "" {
		return false
	}
	for _, s := range c.Skills {
		if strings.ToLower(strings.TrimSpace(s)) == want {
			return true
		}
	}
	return false
}

// Explain 给出该候选人在此工单下的打分明细.
//
// requiredSkill 为空表示"不限技能", 此时所有候选的技能分相同 ——
// 这不是漏洞, 而是刻意设计: 不指定技能时算法自动退化为"就近 + 负载均衡"。
func Explain(targetZone, requiredSkill string, c Candidate) ScoreBreakdown {
	skillMatched := requiredSkill == "" || c.HasSkill(requiredSkill)

	dist := ZoneDistance(targetZone, c.ZoneCode)

	skillPart := 0
	if skillMatched {
		skillPart = skillWeight
	}
	zonePart := zoneWeight * (maxZoneDist - dist)

	load := c.Load
	if load > loadCap {
		load = loadCap
	}
	loadPart := int(loadCap - load)

	return ScoreBreakdown{
		SkillMatched: skillMatched,
		Distance:     dist,
		Load:         c.Load,
		Score:        skillPart + zonePart + loadPart,
	}
}

// Pick 从候选中选出处理人.
//
// 核心保证: **无技能匹配者时不会把工单卡住** —— 全员技能分为 0,
// 算法自动落到"距离 -> 负载"的次优选择, 而不是返回空。
// 池为空时返回 ok=false, 由调用方决定是转人工还是报错。
func Pick(targetZone, requiredSkill string, candidates []Candidate) (Candidate, bool) {
	if len(candidates) == 0 {
		return Candidate{}, false
	}

	best := candidates[0]
	bestScore := Explain(targetZone, requiredSkill, best).Score

	for _, c := range candidates[1:] {
		if s := Explain(targetZone, requiredSkill, c).Score; s > bestScore {
			best, bestScore = c, s
		}
	}
	return best, true
}

// LoadCandidates 从 dispatch_task 的历史指派记录推导候选处理人.
//
// 这是**兜底池**: 当 dispatch_staff 表尚未录入人员时使用, 保证老行为不退化。
// 代价是拿不到技能标签(历史记录里没有该信息), 只能按就近+负载派。
func LoadCandidates(ctx context.Context, db *gorm.DB) ([]Candidate, error) {
	var rows []struct {
		AssigneeId   int64
		AssigneeName string
		ZoneCode     string
		LoadCnt      int64
	}

	// 注意: 聚合列别名不能用 `load` —— 它是 MySQL 保留字, 会导致 1064 语法错误.
	err := db.WithContext(ctx).
		Model(&model.DispatchTask{}).
		Select("assignee_id,"+
			" MAX(assignee_name) AS assignee_name,"+
			" MAX(zone_code) AS zone_code,"+
			" SUM(CASE WHEN status IN ? THEN 1 ELSE 0 END) AS load_cnt",
			[]int8{model.StatusAssigned, model.StatusProcessing}).
		Where("assignee_id > 0").
		Group("assignee_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{
			AssigneeId:   r.AssigneeId,
			AssigneeName: r.AssigneeName,
			ZoneCode:     r.ZoneCode,
			Load:         r.LoadCnt,
		})
	}
	return out, nil
}
