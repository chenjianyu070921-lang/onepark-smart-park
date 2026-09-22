package provider

import (
	"context"
	"testing"

	alarmpb "onepark/proto/alarm"
	devicepb "onepark/proto/device"
	workorderpb "onepark/proto/workorder"
)

// 本文件专测**契约漂移**: 上游新增取值 / 改量纲时, M5 会怎样。
//
// 与其它用例的分工:
//   - alarm_test / device_test 钉的是「正常数据映射对不对」(四项分别等于几);
//   - 本文件钉的是「上游变了之后, 我们**能不能发现**」。
//
// 为什么单独立一个文件: 前者是功能测试, 后者是**防线测试** ——
// 防线测试的价值不在覆盖率, 而在"上游改契约时它会红"。

// TestAlarmStat_DriftDetectableWhenUpstreamAddsLevel M3 新增告警等级时, 漂移必须能从返回值本身看出来。
//
// 场景: M3 给 alarm.level 增加了一个新等级码(9)。M5 的四项只覆盖 1~4,
// 那批告警会**落在四项之外** —— 大屏上"总 10 条, 分组加起来 3 条"。
//
// 断言两件事:
//  1. 未知等级**不会**被塞进任何语义字段(不制造"看起来对"的假数据);
//  2. 差额**能从 DTO 自身算出来** —— 适配器内部的 warnIfDrifted 判断的就是它,
//     所以这条断言也等价于"日志一定会被触发"。
//
// (「未知等级不污染语义字段」由 alarm_test 的 TestAlarmStat_UnknownLevelIgnored 单独钉住,
// 本用例不重复那部分, 只补"漂移可检测"这一层。)
func TestAlarmStat_DriftDetectableWhenUpstreamAddsLevel(t *testing.T) {
	fake := &fakeAlarmServer{resp: &alarmpb.GetActiveAlarmsResp{
		Total:      10,
		LevelCount: map[int32]int64{1: 3, 9: 7}, // 9 = 上游新增等级, 不在 M5 映射表内
	}}
	p := NewAlarm(newAlarmClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}

	sum := stat.Critical + stat.Major + stat.Minor + stat.Info
	if sum == stat.Total {
		t.Fatalf("分项之和 = %d 恰好等于 Total, 本用例想构造的漂移没构造出来", sum)
	}
	if diff := stat.Total - sum; diff != 7 {
		t.Errorf("差额 = %d, 期望 7(全部是未知等级 9 的那批) —— "+
			"差额本身是可观测信号, 数值不对说明映射或统计口径另有变化", diff)
	}
}

// TestDeviceStat_DriftDetectableWhenUpstreamAddsState M1 新增设备状态时, 差额必须是可见的。
//
// 场景比 device_test 里那个"上游自己写错 90+1!=100"更真实: M1 给 device.status
// 加了一个新取值(例如"维护中"), 于是有 5 台设备**在 M5 的契约里无处安放**。
//
// 这正是本用例要防的形态: 总数 100 台, 大屏上网+离+障 = 95 台, 少的那 5 台
// 既不在线也不离线也不故障 —— 没有这条断言, 它只会表现为"数字有点怪"。
func TestDeviceStat_DriftDetectableWhenUpstreamAddsState(t *testing.T) {
	fake := &fakeDeviceServer{resp: &devicepb.GetDeviceStatResp{
		Total:   100,
		Online:  80,
		Offline: 10,
		Fault:   5, // 80 + 10 + 5 = 95, 另 5 台落在契约外
	}}
	p := NewDevice(newDeviceClient(t, fake))

	stat, err := p.Stat(context.Background())
	if err != nil {
		t.Fatalf("Stat 不应返回错误: %v", err)
	}

	sum := stat.Online + stat.Offline + stat.Fault
	if sum >= stat.Total {
		t.Fatalf("分项之和 = %d >= Total %d, 本用例想构造的漂移没构造出来", sum, stat.Total)
	}
	if diff := stat.Total - sum; diff != 5 {
		t.Errorf("差额 = %d, 期望 5 —— 即「上游新增取值」的规模, 运营可据此判断影响面", diff)
	}
}

// TestWorkOrderStat_CompleteRateBoundaries 完成率的两个端点与中点。
//
// M2 给 0~100 的百分数, 大屏要 0~1 的比例 —— 这是本端唯一的"加工"动作, 也是最容易错的一步
// (除以 100 时整数/浮点、端点是否包含)。端点错一位, 大屏上"全部完成"会显示成 0%。
func TestWorkOrderStat_CompleteRateBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		upstrm  float64 // M2 口径: 0~100
		want    float64
		wantStr string
	}{
		{"全未完成", 0, 0, "0"},
		{"半数", 50, 0.5, "0.5"},
		{"全部完成", 100, 1, "1(不能是 0 或 100)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWorkOrderServer{resp: &workorderpb.ListWorkOrdersResp{CompletionRate: c.upstrm}}
			p := NewWorkOrder(newWorkOrderClient(t, fake))

			stat, err := p.Stat(context.Background(), 0)
			if err != nil {
				t.Fatalf("Stat 不应返回错误: %v", err)
			}
			if diff := stat.CompleteRate - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("上游 %v 应换算为 %v, 实际 %v", c.upstrm, c.wantStr, stat.CompleteRate)
			}
		})
	}
}

// TestWorkOrderStat_CompleteRateOutOfRangePassThrough 上游给出越界完成率时**透传, 不压制**。
//
// 为什么不在 M5 侧 clamp 到 1: 那会把"M2 口径变了"这件事变成"看起来正常" ——
// 与 device 的 TestDeviceStat_PassThroughOnInconsistency 同一原则:
// 数据不纠正(真相源在上游), 但由 warnIfRateOutOfRange 留一条日志,
// 让"完成率 150%"这种一眼假的数字**有人认领**。
func TestWorkOrderStat_CompleteRateOutOfRangePassThrough(t *testing.T) {
	fake := &fakeWorkOrderServer{resp: &workorderpb.ListWorkOrdersResp{CompletionRate: 150}}
	p := NewWorkOrder(newWorkOrderClient(t, fake))

	stat, err := p.Stat(context.Background(), 0)
	if err != nil {
		t.Fatalf("Stat 不应因上游口径异常而报错(整体可用性优先): %v", err)
	}
	if diff := stat.CompleteRate - 1.5; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CompleteRate = %v, 期望 1.5 —— 越界值应原样透传(留日志)而不是被 clamp 掩盖", stat.CompleteRate)
	}
}
