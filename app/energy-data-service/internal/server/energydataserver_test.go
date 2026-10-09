package server

import (
	"context"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/energy-data-service/internal/config"
	"onepark/app/energy-data-service/internal/model"
	"onepark/app/energy-data-service/internal/svc"
	energypb "onepark/proto/energy"
)

// 日报告生成(接口54)的测试: 直接调 EnergyDataServer 的方法, 不需要起 gRPC 端口。
// 数据用 UTS- 前缀 + UT区, 跑前跑后清理, 不碰真实数据; 连不上库就跳过。
// 前缀和 model 包的 UTM- 分开: go test 按包并行, 共用前缀会互相删数据。

var (
	utDay     = time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)
	utNextDay = utDay.AddDate(0, 0, 1)
)

func newTestServer(t *testing.T) *EnergyDataServer {
	var c config.Config
	if err := conf.Load("../../etc/energydata-api.yaml", &c); err != nil {
		t.Skipf("读不到配置文件, 跳过: %v", err)
	}
	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		t.Skipf("连不上数据库, 跳过: %v", err)
	}
	clean := func() { db.Exec("DELETE FROM energy_reading WHERE device_id LIKE 'UTS-%'") }
	clean()
	t.Cleanup(clean)

	seed := func(device string, kwh float64, at time.Time) {
		r := &model.EnergyReading{DeviceID: device, ZoneID: "UT区", EnergyKwh: kwh, ReportedAt: at}
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("造数据失败: %v", err)
		}
	}
	// 当天两台设备: 70 + 10 = 80 度; 另有一条前一天的读数不该算进来
	seed("UTS-1", 100, utDay.Add(-10*time.Minute))
	seed("UTS-1", 110, utDay.Add(10*time.Minute))
	seed("UTS-1", 180, utDay.Add(23*time.Hour+50*time.Minute))
	seed("UTS-2", 5000, utDay.Add(30*time.Minute))
	seed("UTS-2", 5010, utDay.Add(12*time.Hour))

	svcCtx := &svc.ServiceContext{EnergyReading: model.NewEnergyReadingModel(db)}
	return NewEnergyDataServer(svcCtx)
}

// TestGetDailyReportTotal 日报总量 = 各设备(期末-期初)求和, 且带最新数据时间
// 远程共享库偶发抖动, 断言前轮询最多 15 秒等种子数据可见
func TestGetDailyReportTotal(t *testing.T) {
	s := newTestServer(t)

	deadline := time.Now().Add(15 * time.Second)
	var resp *energypb.GetDailyReportResponse
	var err error
	for {
		resp, err = s.GetDailyReport(context.Background(), &energypb.GetDailyReportRequest{
			Date:   "2026-09-10",
			ZoneId: "UT区",
		})
		if err == nil && resp.GetTotalUsageKwh() == 80 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等了 15 秒日报告总量还不是 80 (最后 %v, err=%v)", resp.GetTotalUsageKwh(), err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	if resp.GetDate() != "2026-09-10" {
		t.Errorf("回带日期错: %s", resp.GetDate())
	}
	if resp.GetUpdatedAt() == "" {
		t.Error("updated_at 应该回带最新一条数据的时间")
	}
}

// TestGetDailyReportEmpty 没数据的天不算错, 返回 0
func TestGetDailyReportEmpty(t *testing.T) {
	s := newTestServer(t)

	resp, err := s.GetDailyReport(context.Background(), &energypb.GetDailyReportRequest{
		Date:   "2026-09-09",
		ZoneId: "UT区",
	})
	if err != nil {
		t.Fatalf("没数据不应该报错: %v", err)
	}
	if resp.GetTotalUsageKwh() != 0 {
		t.Errorf("没数据应该返回 0, 实际 %v", resp.GetTotalUsageKwh())
	}
}

// TestGetDailyReportBadDate 日期格式错误要返回 InvalidArgument
func TestGetDailyReportBadDate(t *testing.T) {
	s := newTestServer(t)

	_, err := s.GetDailyReport(context.Background(), &energypb.GetDailyReportRequest{Date: "abc"})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Fatalf("应该返回 InvalidArgument, 实际: %v", err)
	}
}
