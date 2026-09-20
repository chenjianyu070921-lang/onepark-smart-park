package mq

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/energy-data-service/internal/config"
	"onepark/app/energy-data-service/internal/model"
)

// newTestConsumer 造一个只测解析逻辑的消费者(不连 Kafka)
// 读不到配置或连不上库就跳过, 免得没环境时单元测试红一片
func newTestConsumer(t *testing.T) *Consumer {
	var c config.Config
	if err := conf.Load("../../etc/energydata-api.yaml", &c); err != nil {
		t.Skipf("读不到配置文件, 跳过: %v", err)
	}
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		t.Skipf("连不上数据库, 跳过: %v", err)
	}
	zone := c.DefaultZone
	if zone == "" {
		zone = "未分配"
	}
	return &Consumer{
		model:       model.NewEnergyReadingModel(db),
		devices:     model.NewDeviceModel(db),
		defaultZone: zone,
		zoneCache:   make(map[string]string),
	}
}

// TestParseEnergy 电表消息能正确解析
func TestParseEnergy(t *testing.T) {
	c := newTestConsumer(t)
	ts := time.Now().Unix()

	raw := `{"request_id":"r1","device_id":"METER-A01","device_type":"meter",` +
		`"event_type":"telemetry","occurred_at":` + f(ts) + `,` +
		`"payload":{"energy_kwh":12345.6,"power_kw":1.2,"zone_id":"A栋"},"source":"mqtt"}`

	r, ok, reason := c.parse(context.Background(), []byte(raw))
	if !ok {
		t.Fatalf("应该入库, 实际被跳过: %s", reason)
	}
	if r.DeviceID != "METER-A01" {
		t.Errorf("设备号错: %s", r.DeviceID)
	}
	if r.ZoneID != "A栋" {
		t.Errorf("区域应该取 payload 里的 A栋, 实际: %s", r.ZoneID)
	}
	if r.EnergyKwh != 12345.6 {
		t.Errorf("读数错: %v", r.EnergyKwh)
	}
	if r.PowerKw == nil || *r.PowerKw != 1.2 {
		t.Errorf("功率错: %v", r.PowerKw)
	}
	if r.ReportedAt.Unix() != ts {
		t.Errorf("时间应该用信封上的 occurred_at, 期望 %d 实际 %d", ts, r.ReportedAt.Unix())
	}
}

// TestParseZoneFallback 没上报区域时, 走 device 表或默认区域, 不能丢数据
func TestParseZoneFallback(t *testing.T) {
	c := newTestConsumer(t)

	raw := `{"device_id":"MOCK-NOZONE","device_type":"meter","event_type":"telemetry",` +
		`"occurred_at":` + f(time.Now().Unix()) + `,"payload":{"energy_kwh":7777}}`

	r, ok, reason := c.parse(context.Background(), []byte(raw))
	if !ok {
		t.Fatalf("不应该丢弃: %s", reason)
	}
	if r.ZoneID == "" {
		t.Error("区域不能是空串, 至少要兜底到默认区域")
	}
	t.Logf("未上报区域的设备归入: %s", r.ZoneID)
}

// TestParseNotEnergy 地磁/门禁这类非能耗事件要静默跳过, 不能报脏数据刷屏
func TestParseNotEnergy(t *testing.T) {
	c := newTestConsumer(t)

	raw := `{"device_id":"SENSOR-01","device_type":"geomagnetic","event_type":"parking",` +
		`"occurred_at":` + f(time.Now().Unix()) + `,"payload":{"occupied":true,"battery":87}}`

	_, ok, reason := c.parse(context.Background(), []byte(raw))
	if ok {
		t.Fatal("非能耗事件不应该入库")
	}
	if reason != "" {
		t.Errorf("非能耗事件属于正常跳过, 不该有丢弃原因(否则日志会被刷屏), 实际: %s", reason)
	}
}

// TestParseDirty 脏数据要丢掉并说明原因
func TestParseDirty(t *testing.T) {
	c := newTestConsumer(t)
	ctx := context.Background()

	cases := []struct{ name, raw string }{
		{"缺少设备号", `{"device_type":"meter","occurred_at":1,"payload":{"energy_kwh":100}}`},
		{"负读数", `{"device_id":"BAD","occurred_at":1,"payload":{"energy_kwh":-1}}`},
		{"不是JSON", `这不是JSON`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, reason := c.parse(ctx, []byte(tc.raw))
			if ok {
				t.Fatalf("%s 应该被丢弃", tc.name)
			}
			if reason == "" {
				t.Errorf("%s 应该给出丢弃原因", tc.name)
			}
		})
	}
}

// TestParseMillisTimestamp 有的设备上报毫秒级时间戳, 要能兜住
func TestParseMillisTimestamp(t *testing.T) {
	c := newTestConsumer(t)
	millis := time.Now().UnixMilli()
	want := millis / 1000

	raw := `{"device_id":"METER-MS","occurred_at":` + f(millis) + `,"payload":{"energy_kwh":100}}`
	r, ok, _ := c.parse(context.Background(), []byte(raw))
	if !ok {
		t.Fatal("毫秒时间戳应该能解析")
	}
	if r.ReportedAt.Unix() != want {
		t.Errorf("毫秒时间戳解析错, 期望 %d 实际 %d", want, r.ReportedAt.Unix())
	}
}

func f(n int64) string { return strconv.FormatInt(n, 10) }
