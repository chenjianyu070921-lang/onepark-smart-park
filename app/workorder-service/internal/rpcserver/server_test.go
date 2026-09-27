package rpcserver

import (
	"testing"

	workorderpb "onepark/proto/workorder"
)

// TestStatusFilter 验证 gRPC 状态筛选约定与 HTTP 对齐(修复审查问题1):
//   - status < 0 表示不限(默认);
//   - status >= 0 表示按该状态筛选, 其中 0 = 待派单.
// 原约定 status=0 为"不限"会导致 M5 大屏按待派单筛选时拿到全量.
func TestStatusFilter(t *testing.T) {
	// 不限: 负数哨兵
	if apply, _ := statusFilter(&workorderpb.ListWorkOrdersReq{Status: -1}); apply {
		t.Error("status=-1 应表示不限(apply=false)")
	}
	// 待派单(0): 与 HTTP ListWorkOrders 的 *int8 nil/0 语义对齐
	if apply, st := statusFilter(&workorderpb.ListWorkOrdersReq{Status: 0}); !apply || st != 0 {
		t.Errorf("status=0 应筛选待派单, apply=%v st=%d", apply, st)
	}
	// 其它状态(如已完成=3)原样透传
	if apply, st := statusFilter(&workorderpb.ListWorkOrdersReq{Status: 3}); !apply || st != 3 {
		t.Errorf("status=3 应筛选已完成, apply=%v st=%d", apply, st)
	}
}

// TestDeriveCompletionRate 覆盖完成率派生(纯函数, 不依赖 DB).
//
// 这是服务端聚合里唯一可脱离数据库的"加工"步骤: 真正的 SUM/COUNT/AVG 在 SQL 中,
// 此函数只负责"除法 + 空园区除零保护"。把它抽成纯函数后, 完成率口径(含已关闭终态)
// 与零值边界就有了确定性的回归保护, 无需起 MySQL。
func TestDeriveCompletionRate(t *testing.T) {
	cases := []struct {
		name       string
		done, total int64
		want       float64
	}{
		{"空园区不除零", 0, 0, 0},
		{"零完成", 0, 10, 0},
		{"七成完成", 7, 10, 70},
		{"全完成", 10, 10, 100},
		// 完成率含"已关闭"终态: 4 单中 3 已完成 + 1 已关闭 => 100%
		{"已关闭计入终态", 4, 4, 100},
		// 4 单中 3 单终态(2 已完成 + 1 已关闭), 完成率 75%
		{"含已关闭口径", 3, 4, 75},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveCompletionRate(c.done, c.total); got != c.want {
				t.Errorf("deriveCompletionRate(%d,%d) = %v, 期望 %v", c.done, c.total, got, c.want)
			}
		})
	}
}
