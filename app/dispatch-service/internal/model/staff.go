package model

import (
	"strings"
	"time"
)

// 人员在岗/启停状态.
const (
	StaffOffDuty int8 = 0 // 不在岗
	StaffOnDuty  int8 = 1 // 在岗

	StaffDisabled int8 = 0 // 停用
	StaffEnabled  int8 = 1 // 启用
)

// DispatchStaff 调度人员技能池, 是智能派单的候选来源.
//
// 为什么由 M5 自持: proto/user 与 proto/auth 至今仍是 rpc Ping 骨架,
// 团队没有人员主数据接口; 待 M6 提供后改为同步, 本结构与算法不变。
type DispatchStaff struct {
	Id        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	StaffId   int64     `gorm:"column:staff_id;uniqueIndex"`
	Name      string    `gorm:"column:name;size:64"`
	Phone     string    `gorm:"column:phone;size:32"`
	ZoneCode  string    `gorm:"column:zone_code;size:64"`
	Skills    string    `gorm:"column:skills;size:255"` // 逗号分隔, 如 fire,electrical
	OnDuty    int8      `gorm:"column:on_duty"`
	Status    int8      `gorm:"column:status"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName 指定表名.
func (DispatchStaff) TableName() string { return "dispatch_staff" }

// SkillList 解析技能标签; 自动去空白与空项, 避免 "fire, ,electrical" 这类脏数据影响匹配.
func (s *DispatchStaff) SkillList() []string {
	if strings.TrimSpace(s.Skills) == "" {
		return nil
	}
	raw := strings.Split(s.Skills, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if v := strings.TrimSpace(r); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// NormalizeSkills 归一化技能标签串: 去空白、去空项、去重, 统一小写.
// 写入前调用, 保证 "Fire, fire ,electrical" 不会因为大小写或空格匹配不上。
func NormalizeSkills(skills string) string {
	if strings.TrimSpace(skills) == "" {
		return ""
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, r := range strings.Split(skills, ",") {
		v := strings.ToLower(strings.TrimSpace(r))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return strings.Join(out, ",")
}
