package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"

	"onepark/app/dispatch-service/internal/config"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/common/gormx"
	"onepark/common/middleware"
	"onepark/common/redisx"
)

// TestUI_DispatchActionsContract 用**服务自己的路由表**起一个进程内 HTTP 服务,
// 走一遍前端「调度工单」页会发的调用序列:
//
//	列人员 -> 建单 -> 指派(自动) -> 开始 -> 完成 -> 再开始(应被拒)
//
// 为什么必须有这一层:
//   - 前端按钮的 路径/方法/字段名 必须与后端逐字一致。这类"接线"错误在浏览器里
//     只表现为"点了没反应", 定位成本极高; 而这里照路由表发真请求, 错一个字母就红。
//   - 同时钉住状态机的**对外行为**: 已完成的工单再 start 必须被拒 ——
//     否则前端那几颗按钮只是装饰, 真正拦得住的是后端。
//   - 顺带钉住**租户隔离**: 拿另一个租户去读/改这张单, 必须看不到也改不动。
//
// ⚠️ 响应形状(踩过一次, 写在这免得后人再猜):
//   - **成功**: handler 走 httpx.OkJsonCtx, 直接输出 payload, **没有 {code,msg,data} 外壳**;
//   - **失败**: go-zero 的错误处理器输出的是**纯文本** "M5-E-2001: 调度工单不存在"
//     (不是 JSON!) —— 所以判断成败不能靠 json.Unmarshal 是否成功。
//
// 两个刻意的设计:
//   - 用**唯一租户**(纳秒)建单, 不污染演示数据; 结束时按 id 精确删除自己造的那张单;
//   - 状态一律**回查详情**确认, 不依赖各变更接口自己的响应结构(少一处猜测就少一处会过时的断言)。
func TestUI_DispatchActionsContract(t *testing.T) {
	cfgPath := "../../etc/dispatch-api.yaml"
	if _, err := os.Stat(cfgPath); err != nil {
		t.Skip("跳过: 未找到 etc/dispatch-api.yaml")
	}
	var c config.Config
	// UseEnv() 必开: 配置里的 DSN/Redis 是 ${VAR} 占位符, 不展开则连的是字面量。
	if err := conf.Load(cfgPath, &c, conf.UseEnv()); err != nil {
		t.Skipf("跳过: 配置加载失败: %v", err)
	}
	if c.MySQL.DataSource == "" {
		t.Skip("跳过: 未设置 DISPATCH_MYSQL_DSN")
	}

	db, err := gormx.NewDB(c.MySQL.DataSource)
	if err != nil {
		t.Skipf("跳过: MySQL 不可用: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Skipf("跳过: 取 sql.DB 失败: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Skipf("跳过: MySQL Ping 失败: %v", err)
	}
	rdb := redisx.NewClient(&c.Redis)
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelPing()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		t.Skipf("跳过: Redis 不可用: %v", err)
	}

	// 用真实 RestConf(名字/超时/日志都合法), 只覆盖地址与端口 —— 比手搓一个 RestConf 稳。
	port := freePort(t)
	c.RestConf.Host = "127.0.0.1"
	c.RestConf.Port = port

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()
	// 与 dispatch.go 的中间件顺序保持一致(顺序错了行为就不同)
	server.Use(middleware.RequestIdMiddleware)
	server.Use(middleware.Cors)
	server.Use(middleware.IdentityFromHeader)

	svcCtx := &svc.ServiceContext{Config: c, DB: db, Redis: rdb}
	RegisterHandlers(server, svcCtx)
	go server.Start()

	base := fmt.Sprintf("http://127.0.0.1:%d/api", port)
	waitHealthy(t, base)

	// 本用例自始至终用**同一个**唯一租户: 跨租户访问另用 defaultCaller 验证。
	tenant := time.Now().UnixNano()
	me := newCaller(t, base, tenant)
	other := newCaller(t, base, 1) // 演示库里的默认园区

	// ---------- 1. 人员候选(指派对话框的数据源) ----------
	var staffs struct {
		Total int64 `json:"total"`
		List  []struct {
			StaffId int64  `json:"staff_id"`
			Name    string `json:"name"`
		} `json:"list"`
	}
	raw := me.call(t, http.MethodGet, "/dispatch/staffs?on_duty=1&page=1&page_size=100", nil)
	mustSucceed(t, raw)
	mustDecode(t, raw, &staffs)
	t.Logf("在岗人员 %d 人", staffs.Total)
	if staffs.Total == 0 {
		t.Skip("跳过: 库里没有在岗人员, 自动指派必然无人可派(演示环境应先跑 m5_seed.ps1)")
	}

	// ---------- 2. 建单 ----------
	raw = me.call(t, http.MethodPost, "/dispatch", map[string]any{
		"title":          "UI 契约验证单(自动清理)",
		"source":         1, // 1 = 人工创建
		"zone_code":      "Z-2-101",
		"required_skill": "",
		"priority":       3,
	})
	mustSucceed(t, raw)
	var created struct {
		Id     int64  `json:"id"`
		TaskNo string `json:"task_no"`
		Status int64  `json:"status"`
	}
	mustDecode(t, raw, &created)
	if created.Id == 0 {
		t.Fatalf("建单返回的 id 为 0, body=%s", raw)
	}
	taskID := created.Id
	t.Logf("已建单 id=%d no=%s status=%d", taskID, created.TaskNo, created.Status)
	t.Cleanup(func() {
		// 只删本用例造的那张单(连带它的审计流水), 不留测试数据在演示库里
		_ = db.Exec("DELETE FROM dispatch_task_log WHERE task_id = ?", taskID).Error
		_ = db.Exec("DELETE FROM dispatch_task WHERE id = ?", taskID).Error
		t.Logf("已清理测试工单 id=%d", taskID)
	})

	// ---------- 3. 指派(assignee_id=0 走自动指派) ----------
	raw = me.call(t, http.MethodPut, fmt.Sprintf("/dispatch/%d/assign", taskID), map[string]any{
		"assignee_id": 0,
	})
	mustSucceed(t, raw)
	var assigned struct {
		AssigneeId   int64  `json:"assignee_id"`
		AssigneeName string `json:"assignee_name"`
	}
	mustDecode(t, raw, &assigned)
	if assigned.AssigneeId == 0 {
		t.Fatalf("自动指派没有写入处理人, body=%s", raw)
	}
	t.Logf("指派结果: assignee=%s(#%d)", assigned.AssigneeName, assigned.AssigneeId)
	if got := me.taskStatus(t, taskID); got != 2 {
		t.Fatalf("指派后状态应为 2(已指派), 实际 %d", got)
	}

	// ---------- 4. start -> finish(每步都回查详情确认) ----------
	for _, tc := range []struct {
		action string
		want   int64
	}{{"start", 3}, {"finish", 4}} {
		raw = me.call(t, http.MethodPut, fmt.Sprintf("/dispatch/%d/status", taskID), map[string]any{
			"action": tc.action,
		})
		mustSucceed(t, raw)
		if got := me.taskStatus(t, taskID); got != tc.want {
			t.Fatalf("%s 后状态应为 %d, 实际 %d", tc.action, tc.want, got)
		}
		t.Logf("%s -> status=%d", tc.action, tc.want)
	}

	// ---------- 5. 时间线(前端抽屉里渲染的那条) ----------
	var detail struct {
		Task struct {
			Id     int64 `json:"id"`
			Status int64 `json:"status"`
		} `json:"task"`
		Logs []struct {
			Action     string `json:"action"`
			FromStatus int64  `json:"from_status"`
			ToStatus   int64  `json:"to_status"`
		} `json:"logs"`
	}
	raw = me.call(t, http.MethodGet, fmt.Sprintf("/dispatch/%d", taskID), nil)
	mustSucceed(t, raw)
	mustDecode(t, raw, &detail)

	actions := make([]string, 0, len(detail.Logs))
	for _, l := range detail.Logs {
		actions = append(actions, l.Action)
	}
	t.Logf("时间线: %v", actions)
	want := []string{"create", "assign", "start", "finish"}
	if len(actions) < len(want) {
		t.Fatalf("时间线太短, 期望至少 %v, 实际 %v", want, actions)
	}
	for i, w := range want {
		if actions[i] != w {
			t.Fatalf("时间线第 %d 步应为 %s, 实际 %s(完整: %v)", i+1, w, actions[i], actions)
		}
	}

	// ---------- 6. 反向验证: 已完成再 start 必须被拒 ----------
	// 前端 TASK_ALLOWED_ACTIONS 不给这个按钮, 但**后端必须自己拦** —— 不能只靠界面。
	raw = me.call(t, http.MethodPut, fmt.Sprintf("/dispatch/%d/status", taskID), map[string]any{
		"action": "start",
	})
	if code := codeOf(t, raw); code == "0" {
		t.Fatalf("已完成的工单竟然可以再 start —— 状态机没拦住(前端按钮策略也就失去意义), body=%s", raw)
	} else {
		t.Logf("已完成再 start 被拒 ✅ code=%s", code)
	}
	if got := me.taskStatus(t, taskID); got != 4 {
		t.Fatalf("被拒的请求不应改变状态, 期望仍为 4, 实际 %d", got)
	}

	// ---------- 7. 租户隔离: 换个租户, 既看不到也改不动 ----------
	// 这条不是"额外福利": 前端每颗按钮都带 x-tenant-id, 一旦越权, 演示时会串园区数据。
	raw = other.call(t, http.MethodGet, fmt.Sprintf("/dispatch/%d", taskID), nil)
	if code := codeOf(t, raw); code == "0" {
		t.Fatalf("跨租户读到了别人的工单 —— 详情接口没有按 ctx 租户过滤, body=%s", raw)
	} else {
		t.Logf("跨租户读详情被拒 ✅ code=%s", code)
	}
	raw = other.call(t, http.MethodPut, fmt.Sprintf("/dispatch/%d/assign", taskID), map[string]any{
		"assignee_id": 0,
	})
	if code := codeOf(t, raw); code == "0" {
		t.Fatalf("跨租户指派成功 —— 能把别人的工单派给自己的人, body=%s", raw)
	} else {
		t.Logf("跨租户指派被拒 ✅ code=%s", code)
	}
}

// ---------- 测试内的小工具 ----------

type caller struct {
	base   string
	tenant string
	client *http.Client
}

func newCaller(t *testing.T, base string, tenant int64) *caller {
	t.Helper()
	return &caller{
		base:   base,
		tenant: fmt.Sprintf("%d", tenant),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// call 发一次请求并返回原始 body。
// 刻意保留原始 body: 断言失败时要把后端真实返回打出来, 否则又是一轮"猜"。
func (c *caller) call(t *testing.T, method, path string, body any) string {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("x-tenant-id", c.tenant)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s 请求失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		// 路径写错会走到这里, 直接把状态码摊开(这类响应通常不是业务错误)
		t.Fatalf("%s %s -> HTTP 404, 路径可能不对; body=%s", method, path, string(raw))
	}
	return string(raw)
}

// taskStatus 回查详情取当前状态 —— 变更接口的响应结构各不相同, 用它当唯一事实来源。
func (c *caller) taskStatus(t *testing.T, id int64) int64 {
	t.Helper()
	raw := c.call(t, http.MethodGet, fmt.Sprintf("/dispatch/%d", id), nil)
	mustSucceed(t, raw)
	var d struct {
		Task struct {
			Status int64 `json:"status"`
		} `json:"task"`
	}
	mustDecode(t, raw, &d)
	return d.Task.Status
}

// codeOf 从响应里取业务错误码, 取不到(表示成功)则返回 "0"。
//
// 必须同时支持两种形态:
//   - 成功: 直接是 payload(JSON 对象), **没有 code 字段**;
//   - 失败: 纯文本 "M5-E-2001: 调度工单不存在"(go-zero 错误处理器的输出, 不是 JSON),
//     以及少见的 JSON {"code":..,"msg":..}。
func codeOf(t *testing.T, raw string) string {
	t.Helper()
	s := strings.TrimSpace(raw)
	if s == "" {
		return "0"
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return "0" // 是 JSON 但不是错误外壳 -> 当成成功
		}
		v, ok := m["code"]
		if !ok {
			return "0"
		}
		var code string
		if err := json.Unmarshal(v, &code); err != nil || code == "" {
			return "0"
		}
		return code
	}
	// 纯文本: 形如 "M5-E-2001: 调度工单不存在" / "M6-E-0001: ..."
	if i := strings.IndexByte(s, ':'); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// mustSucceed 用于**期望成功**的步骤: 出现业务错误码直接 Fatal。
func mustSucceed(t *testing.T, raw string) {
	t.Helper()
	if code := codeOf(t, raw); code != "0" {
		t.Fatalf("接口返回业务错误: %s, body=%s", code, raw)
	}
}

func mustDecode(t *testing.T, raw string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, raw)
	}
}

// freePort 让系统分配一个空闲端口: 测试并行跑时不会互相抢端口。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitHealthy 等端口可连(go server.Start() 是异步的)。
// 用 /health 只为把"端口已开"变成一次真实 HTTP 往返; 该路由不在 /api 前缀下。
func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("服务在 20s 内没有起来: %s", base)
}
