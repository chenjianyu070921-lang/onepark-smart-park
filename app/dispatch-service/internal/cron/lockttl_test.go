package cron

import (
	"testing"
	"time"
)

// TestReassignLockTTL 锁 TTL 必须**跟着扫描周期走**（原先硬编码 50s，与配置脱钩）。
//
// 两侧边界都要成立：
//
//	不能 >= 周期：异常退出（进程被杀 / 释放时 ctx 已取消）后锁要能在下一轮之前过期，
//	              否则后续几轮扫描被无声跳过，工单一直卡在「已指派」；
//	不能太短：要覆盖单轮扫描窗口，否则另一实例会并发扫同一批，同一张单被派给两个人。
func TestReassignLockTTL(t *testing.T) {
	rows := []struct {
		interval int64
		want     time.Duration
		why      string
	}{
		{60, 50 * time.Second, "默认周期 -> 保持 50s(与改动前的行为一致)"},
		{300, 290 * time.Second, "周期调到 300s 时 TTL 要跟着变长(原硬编码 50s 会留 250s 保护空窗)"},
		{20, 10 * time.Second, "刚好是余量边界: 20-10 = 10s, 不小于 interval/2"},
		{1, 500 * time.Millisecond, "极短周期 -> 触发下界 interval/2, 不能为 0 或负数"},
		{0, 50 * time.Second, "非法配置(0) -> 走兜底周期 60s"},
		{-5, 50 * time.Second, "负数 -> 同上, 绝不能返回负 TTL(负 TTL 的 SETNX 等于没有锁)"},
	}
	for _, r := range rows {
		if got := ReassignLockTTL(r.interval); got != r.want {
			t.Errorf("ReassignLockTTL(%d) = %v, 期望 %v (%s)", r.interval, got, r.want, r.why)
		}
	}

	// 不变量: 任何周期下 TTL 都必须 > 0 且 < 周期
	for _, sec := range []int64{1, 5, 10, 20, 30, 60, 120, 300, 3600} {
		ttl := ReassignLockTTL(sec)
		if ttl <= 0 {
			t.Errorf("周期 %ds 得到非正 TTL %v —— SETNX 用负 TTL 会立刻过期, 等于没有锁", sec, ttl)
		}
		if ttl >= time.Duration(sec)*time.Second {
			t.Errorf("周期 %ds 的 TTL %v 不小于周期 —— 异常退出后会挡住下一轮扫描", sec, ttl)
		}
	}
}
