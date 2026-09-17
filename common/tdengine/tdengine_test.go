package tdengine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestExecSuccess 验证 SQL 内容、鉴权头与库名参数正确传递到 taosAdapter.
func TestExecSuccess(t *testing.T) {
	var gotSQL, gotAuth, gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotSQL, gotAuth, gotURI = string(b), r.Header.Get("Authorization"), r.URL.String()
		_, _ = w.Write([]byte(`{"status":"succ","head":["affected_rows"],"data":[[1]],"rows":1}`))
	}))
	defer srv.Close()

	c := NewClient(Conf{Endpoint: srv.URL, Token: "Basic test", Database: "onepark_ts"})
	if c == nil {
		t.Fatal("期望客户端已创建")
	}
	if err := c.Exec(context.Background(), "INSERT INTO t VALUES (NOW, 1)"); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if gotSQL != "INSERT INTO t VALUES (NOW, 1)" {
		t.Errorf("SQL 未正确透传: %q", gotSQL)
	}
	if gotAuth != "Basic test" {
		t.Errorf("鉴权头未透传: %q", gotAuth)
	}
	if !strings.Contains(gotURI, "db=onepark_ts") {
		t.Errorf("库名参数缺失: %q", gotURI)
	}
}

// TestExecError 验证 TDengine 返回 error 状态时上报为错误.
func TestExecError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","code":9728,"desc":"syntax error"}`))
	}))
	defer srv.Close()

	c := NewClient(Conf{Endpoint: srv.URL})
	if err := c.Exec(context.Background(), "BAD SQL"); err == nil {
		t.Fatal("期望返回错误")
	}
}

// TestWritePoint 验证设备 ID 中的连字符被替换为下划线(TDengine 表名约束)且指标落库.
func TestWritePoint(t *testing.T) {
	var gotSQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotSQL = string(b)
		_, _ = w.Write([]byte(`{"status":"succ","rows":1}`))
	}))
	defer srv.Close()

	c := NewClient(Conf{Endpoint: srv.URL})
	ts := time.UnixMilli(1700000000000)
	if err := c.WritePoint(context.Background(), Point{
		DeviceID: "dev-001", Metric: "temperature", Value: 23.5, Ts: ts,
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if !strings.Contains(gotSQL, "INTO t_dev_001 USING device_telemetry TAGS('dev-001')") {
		t.Errorf("子表名或标签不正确: %q", gotSQL)
	}
	if !strings.Contains(gotSQL, "1700000000000") || !strings.Contains(gotSQL, "'temperature'") {
		t.Errorf("时间戳或指标名不正确: %q", gotSQL)
	}
	if !strings.Contains(gotSQL, "23.5") {
		t.Errorf("指标值不正确: %q", gotSQL)
	}
}

// TestDisabled 验证未配置 Endpoint 时客户端为空实现, 调用返回错误而非 panic.
func TestDisabled(t *testing.T) {
	c := NewClient(Conf{})
	if c != nil {
		t.Fatal("期望客户端为 nil")
	}
	if c.Enabled() {
		t.Error("期望未启用")
	}
	if err := c.WritePoint(context.Background(), Point{DeviceID: "d", Metric: "m"}); err == nil {
		t.Error("期望未启用时返回错误")
	}
}

// TestWritePointParam 验证缺少必填参数时拒绝写入.
func TestWritePointParam(t *testing.T) {
	c := NewClient(Conf{Endpoint: "http://127.0.0.1:1"})
	if err := c.WritePoint(context.Background(), Point{DeviceID: "d"}); err == nil {
		t.Error("期望缺少 metric 时返回错误")
	}
	if err := c.WritePoint(context.Background(), Point{Metric: "m"}); err == nil {
		t.Error("期望缺少 deviceId 时返回错误")
	}
}
