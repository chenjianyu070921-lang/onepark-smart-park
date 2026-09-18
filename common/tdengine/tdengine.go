// Package tdengine 提供 TDengine 时序库写入封装.
// 采用 taosAdapter 的 REST 接口(默认 6041 端口), 不引入原生驱动,
// 避免 CGO 依赖与跨平台编译问题; Endpoint 未配置时自动降级为空实现.
package tdengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Conf TDengine 连接配置.
type Conf struct {
	// Endpoint taosAdapter REST 地址, 如 http://tdengine:6041; 为空表示不启用时序落库
	Endpoint string `json:",env=TDENGINE_REST,optional"`
	// Token 直接作为 Authorization 头透传, 支持 "Basic xxx" 或 "Bearer xxx"
	Token string `json:",env=TDENGINE_TOKEN,optional"`
	// Database 时序库名
	Database string `json:",default=onepark_ts"`
	// Timeout 单次写入超时
	Timeout int `json:",default=3000"`
}

const (
	defaultTimeout = 3 * time.Second
	// TelemetrySTable 通用遥测超级表, 见 deploy/sql/m1_tdengine_tables.sql
	TelemetrySTable = "device_telemetry"
	tablePrefix     = "t_"
)

// Point 一条遥测时序点, 对应 device_telemetry 超级表.
type Point struct {
	DeviceID string  // 设备 ID
	Metric   string  // 指标名: temperature/humidity/pm25/co2/parking...
	Value    float64 // 指标值
	Quality  int8    // 数据质量: 0好 1差 2缺失
	Ts       time.Time
}

// Client TDengine REST 写入客户端.
type Client struct {
	conf Conf
	http *http.Client
}

// NewClient 创建客户端; Endpoint 为空时返回 nil, 表示未启用.
func NewClient(c Conf) *Client {
	if strings.TrimSpace(c.Endpoint) == "" {
		return nil
	}
	if c.Database == "" {
		c.Database = "onepark_ts"
	}
	timeout := defaultTimeout
	if c.Timeout > 0 {
		timeout = time.Duration(c.Timeout) * time.Millisecond
	}
	return &Client{conf: c, http: &http.Client{Timeout: timeout}}
}

// Enabled 是否启用时序落库.
func (c *Client) Enabled() bool {
	return c != nil
}

// Exec 执行一条 SQL, 返回 TDengine 响应中的错误(若有).
func (c *Client) Exec(ctx context.Context, sql string) error {
	if c == nil {
		return errors.New("TDengine 未启用")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.conf.Endpoint, "/")+"/rest/sql?db="+c.conf.Database,
		bytes.NewBufferString(sql))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	if c.conf.Token != "" {
		req.Header.Set("Authorization", c.conf.Token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TDengine 返回状态 %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Desc   string `json:"desc"`
		Code   int    `json:"code"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("解析 TDengine 响应失败: %w", err)
	}
	if result.Status != "" && result.Status != "succ" {
		return fmt.Errorf("TDengine 执行失败: code=%d, desc=%s", result.Code, result.Desc)
	}
	return nil
}

// WritePoint 写入一条遥测点, 自动按设备创建子表.
// 表名为 t_ + deviceID 中的非字母数字替换为下划线(TDengine 表名不支持连字符).
func (c *Client) WritePoint(ctx context.Context, p Point) error {
	if c == nil {
		return errors.New("TDengine 未启用")
	}
	if p.DeviceID == "" || p.Metric == "" {
		return errors.New("遥测点缺少 deviceId 或 metric")
	}
	if p.Ts.IsZero() {
		p.Ts = time.Now()
	}

	return c.Exec(ctx, buildInsertSQL(p))
}

// buildInsertSQL 组装子表写入语句.
// 所有外部输入(deviceId/metric)均做转义, 防止 SQL 注入; 表名另做白名单化.
func buildInsertSQL(p Point) string {
	table := tablePrefix + sanitize(p.DeviceID)
	tag := escapeTDString(p.DeviceID)
	metric := escapeTDString(p.Metric)
	return fmt.Sprintf(
		"INSERT INTO %s USING %s TAGS('%s') VALUES (%d, '%s', %f, %d)",
		table, TelemetrySTable, tag, p.Ts.UnixMilli(), metric, p.Value, p.Quality)
}

// escapeTDString 转义 TDengine 字符串字面量中的反斜杠与单引号.
// TDengine 以 \ 为转义符(\' 表示字面单引号); 必须先转义 \ 再转义 ',
// 否则 "x\' OR 1=1--" 这类输入会闭合字面量越界注入(见 GHSA-h9fj-c2qr-76g2).
func escapeTDString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
