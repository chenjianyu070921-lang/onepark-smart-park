package logic

import "testing"

// 验证访客签入开门降级策略(场景3 验收口径):
// 无论 M1 开门成功与否, 签入流程均不阻断; 降级时提示"请联系前台人工开门".
func TestOpenDoorMsg(t *testing.T) {
	cases := []struct {
		name       string
		configured bool
		opened     bool
		want       string
	}{
		{"开门成功", true, true, openMsgSuccess},
		{"M1已配置但超时/失败-降级", true, false, openMsgDegrade},
		{"M1未配置-降级", false, false, openMsgNoConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := openDoorMsg(c.configured, c.opened); got != c.want {
				t.Errorf("openDoorMsg(%v,%v) = %q, want %q", c.configured, c.opened, got, c.want)
			}
		})
	}
}
