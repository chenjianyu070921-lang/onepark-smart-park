package model

import (
	"errors"
	"fmt"
	"testing"
)

// TestNewTaskNo_NoCollisionWithinOneSecond 同一秒内批量生成单号必须**零重复**。
//
// 这是 2026-09-27 修的 Bug 的核心断言。旧实现是
//
//	DT + 秒级时间戳 + rand.Intn(1000)
//
// 同秒内只有 1000 种取值 —— 下方 900 次调用几乎必然撞号(生日问题),
// 而撞号的后果分两种: 人工建单报错返回 500(可见); **告警自动建单被误判成
// 「告警已建过」静默跳过**(不可见, 消息位移已提交不会重投)。
// 暴露路径: 演示 seed 脚本连建 5 张工单, 挂了 1 张。
func TestNewTaskNo_NoCollisionWithinOneSecond(t *testing.T) {
	const n = 900 // 设计上限 1000 张/秒/进程, 取 900 留余量
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		no := NewTaskNo()
		if _, dup := seen[no]; dup {
			t.Fatalf("第 %d 次生成即重复: %s —— 同秒内必须零重复", i, no)
		}
		seen[no] = struct{}{}
	}
}

// TestNewTaskNo_Format 单号格式必须保持 DT + 14 位时间 + 3 位序号。
// 前端展示与导出都按这个宽度看, 改窄改宽都是对外契约变更。
func TestNewTaskNo_Format(t *testing.T) {
	no := NewTaskNo()
	if len(no) != 2+14+3 {
		t.Fatalf("单号长度 = %d, 期望 19: %s", len(no), no)
	}
	if no[:2] != "DT" {
		t.Fatalf("单号前缀应为 DT: %s", no)
	}
}

// TestIsDuplicateKeyOn 必须**按索引名**区分 1062 —— 混为一谈正是那个静默丢单 Bug 的成因。
func TestIsDuplicateKeyOn(t *testing.T) {
	const (
		idxAlarm = "uk_alarm_id"
		idxNo    = "uk_task_no"
	)
	dupOn := func(table, index string) error {
		return fmt.Errorf("Error 1062 (23000): Duplicate entry 'x' for key '%s.%s'", table, index)
	}

	rows := []struct {
		name  string
		err   error
		index string
		want  bool
	}{
		{"告警重投撞 uk_alarm_id", dupOn("dispatch_task", idxAlarm), idxAlarm, true},
		{"单号撞 uk_task_no", dupOn("dispatch_task", idxNo), idxNo, true},
		// ⭐ 这一行就是 Bug 本体: 拿 uk_alarm_id 去判定单号冲突 -> 必须返回 false,
		//    否则自动建单会把"单号撞了没建成"当成"告警早就建过"而丢弃
		{"单号冲突不能用 uk_alarm_id 判定", dupOn("dispatch_task", idxNo), idxAlarm, false},
		{"别的唯一键不参与判定", dupOn("dispatch_task", "uk_other"), idxNo, false},
		{"非 1062 错误", errors.New("dial tcp: connection refused"), idxNo, false},
		{"nil 错误", nil, idxNo, false},
	}
	for _, r := range rows {
		if got := IsDuplicateKeyOn(r.err, r.index); got != r.want {
			t.Errorf("%s: IsDuplicateKeyOn(%v, %q) = %v, 期望 %v", r.name, r.err, r.index, got, r.want)
		}
	}
}
