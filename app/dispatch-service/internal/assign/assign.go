// Package assign 实现调度工单的指派策略.
//
// 策略(MVP, 不依赖人员主数据表):
//  1. 候选池来自本服务的历史指派记录 —— 因此无需跨库读 M6 的用户表(每服务独立库, 禁止跨库查询)
//  2. 「就近」用区域编码的拓扑距离代替经纬度: 园区场景下 zone_code 形如 A-3F-301,
//     逐段比较即可表达"同房间 / 同楼层 / 同楼栋 / 跨楼栋", 零依赖且可单测
//  3. 「负载」用当前在手工单数(已指派 + 处理中)
package assign

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
)

// Candidate 候选处理人及其当前负载.
type Candidate struct {
	AssigneeId   int64
	AssigneeName string
	ZoneCode     string // 最近一次被指派的区域, 用于就近比较
	Load         int64  // 当前在手工单数
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

// Pick 从候选中选出处理人: 先比拓扑距离(近者优先), 距离相同再比负载(少者优先).
// 池为空时返回 ok=false.
func Pick(targetZone string, candidates []Candidate) (Candidate, bool) {
	if len(candidates) == 0 {
		return Candidate{}, false
	}

	best := candidates[0]
	bestDist := ZoneDistance(targetZone, best.ZoneCode)

	for _, c := range candidates[1:] {
		d := ZoneDistance(targetZone, c.ZoneCode)
		if d < bestDist || (d == bestDist && c.Load < best.Load) {
			best, bestDist = c, d
		}
	}
	return best, true
}

// LoadCandidates 从 dispatch_task 中推导候选处理人及其负载.
// 说明: MAX(assignee_name) 仅用于从历史记录里取出一个可用姓名, 不作为"最新"语义依赖.
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
