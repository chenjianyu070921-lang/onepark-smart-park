package rpcserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/zeromicro/go-zero/core/conf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"onepark/app/leasing-service/internal/config"
	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	commonpb "onepark/proto/common"
	leasingpb "onepark/proto/leasing"
	"onepark/common/gormx"
)

// mustDecimal 构造 decimal, 失败直接 Fatal(测试数据是字面量, 失败即代码写错).
func mustDecimal(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("非法 decimal %q: %v", s, err)
	}
	return d
}

// 本文件是**真实链路**测试: 起真 gRPC 服务(随机端口) + 真客户端 + 真 MySQL,
// 不是纯函数单测 —— 因为本层最容易出事的恰恰是"跨进程边界"上的东西:
// 序列化、租户校验、错误能否正确穿过 gRPC 传回调用方。

// openTestDB 复用服务自身 etc/leasing-api.yaml 的 DSN 连本地 MySQL; 不可用则跳过.
func openTestDB(t *testing.T) *gormx.DB {
	t.Helper()

	for _, p := range []string{"../../etc/leasing-api.yaml", "../../../etc/leasing-api.yaml"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		var c config.Config
		if err := conf.Load(p, &c); err != nil {
			continue
		}
		if c.MySQL.DataSource == "" {
			continue
		}
		db, err := gormx.NewDB(c.MySQL.DataSource)
		if err != nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = sqlDB.PingContext(ctx)
		cancel()
		if err != nil {
			continue
		}
		return db
	}

	t.Skip("跳过: 未找到可用的 etc/leasing-api.yaml, 或本地 MySQL 不可用")
	return nil
}

// startTestGRPC 起真实 gRPC 服务(随机端口)并返回客户端 —— 走完整网络栈.
func startTestGRPC(t *testing.T, svcCtx *svc.ServiceContext) leasingpb.LeasingServiceClient {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}

	srv := grpc.NewServer()
	leasingpb.RegisterLeasingServiceServer(srv, NewLeasingServer(svcCtx))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return leasingpb.NewLeasingServiceClient(conn)
}

// TestPing 探活必须可用 —— 它不依赖 DB, 是服务"活着"的唯一判据.
func TestPing(t *testing.T) {
	cli := startTestGRPC(t, &svc.ServiceContext{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, &commonpb.Empty{}); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
}

// TestGetContract_InvalidId 非法入参必须在服务端被拦下, 而不是拿去查库.
func TestGetContract_InvalidId(t *testing.T) {
	cli := startTestGRPC(t, &svc.ServiceContext{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, id := range []int64{0, -1} {
		_, err := cli.GetContract(ctx, &leasingpb.GetContractReq{Id: id})
		if err == nil {
			t.Fatalf("id=%d 应被拒绝", id)
		}
		if code := status.Code(err); code != codes.Unknown && code != codes.InvalidArgument {
			t.Errorf("id=%d 错误码 = %v, 期望 InvalidArgument(或 errorx 透传为 Unknown)", id, code)
		}
	}
}

// TestGetContract_EndToEnd 真实查询: 落库 -> 经 gRPC 读回 -> 逐字段核对.
func TestGetContract_EndToEnd(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// 用纳秒做唯一后缀, 避免与库里既有数据/并行测试冲突
	suffix := time.Now().UnixNano() % 1_000_000_000
	contract := &model.LeaseContract{
		ContractNo:      fmt.Sprintf("LC-RPC-%d", suffix),
		TenantId:        9_000_000_000 + suffix%1000,
		TenantName:      "RPC 测试租户",
		ZoneCode:        "A-9F-901",
		AreaSqm:         123.45,
		MonthlyRent:     mustDecimal(t, "10000.50"),
		Deposit:         mustDecimal(t, "20001.00"),
		StartDate:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
		EndDate:         time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local),
		Status:          model.StatusActive,
		AutoRenew:       model.AutoRenewOn,
		RenewNoticeDays: 30,
	}
	if err := db.WithContext(ctx).Create(contract).Error; err != nil {
		t.Fatalf("准备合同失败: %v", err)
	}
	t.Cleanup(func() {
		db.WithContext(context.Background()).Delete(&model.LeaseContract{}, contract.Id)
	})

	cli := startTestGRPC(t, &svc.ServiceContext{DB: db})
	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1) 不带租户: 正常返回
	resp, err := cli.GetContract(callCtx, &leasingpb.GetContractReq{Id: contract.Id})
	if err != nil {
		t.Fatalf("GetContract 失败: %v", err)
	}
	got := resp.GetContract()
	if got == nil {
		t.Fatal("contract 为空")
	}
	if got.GetId() != contract.Id {
		t.Errorf("id = %d, 期望 %d", got.GetId(), contract.Id)
	}
	if got.GetContractNo() != contract.ContractNo {
		t.Errorf("contract_no = %q, 期望 %q", got.GetContractNo(), contract.ContractNo)
	}
	if got.GetZoneCode() != "A-9F-901" {
		t.Errorf("zone_code = %q", got.GetZoneCode())
	}
	if got.GetAreaSqm() != 123.45 {
		t.Errorf("area_sqm = %v, 期望 123.45", got.GetAreaSqm())
	}
	// 金额必须逐字相等 —— 这是"用 string 承载 decimal"的意义所在
	if got.GetMonthlyRent() != "10000.50" {
		t.Errorf("monthly_rent = %q, 期望 %q(精度不得丢失)", got.GetMonthlyRent(), "10000.50")
	}
	if got.GetDeposit() != "20001.00" {
		t.Errorf("deposit = %q, 期望 %q", got.GetDeposit(), "20001.00")
	}
	if got.GetStartDate() != "2026-01-01" || got.GetEndDate() != "2026-12-31" {
		t.Errorf("日期 = %q ~ %q", got.GetStartDate(), got.GetEndDate())
	}
	if got.GetStatus() != int32(model.StatusActive) {
		t.Errorf("status = %d, 期望 %d", got.GetStatus(), model.StatusActive)
	}
	if !got.GetAutoRenew() {
		t.Error("auto_renew 应为 true(DB 里是 1)")
	}
	if got.GetRenewNoticeDays() != 30 {
		t.Errorf("renew_notice_days = %d, 期望 30", got.GetRenewNoticeDays())
	}

	// 2) 带正确租户: 放行
	if _, err := cli.GetContract(callCtx, &leasingpb.GetContractReq{
		Id: contract.Id, TenantId: contract.TenantId,
	}); err != nil {
		t.Errorf("同租户查询应放行, 实际: %v", err)
	}

	// 3) 带错误租户: 必须拒绝 —— 这是本层最要紧的一条隔离
	if _, err := cli.GetContract(callCtx, &leasingpb.GetContractReq{
		Id: contract.Id, TenantId: contract.TenantId + 1,
	}); err == nil {
		t.Error("跨租户查询应被拒绝")
	}
}

// TestGetContract_NotFound 不存在的合同应报错而不是返回空对象.
func TestGetContract_NotFound(t *testing.T) {
	db := openTestDB(t)
	cli := startTestGRPC(t, &svc.ServiceContext{DB: db})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cli.GetContract(ctx, &leasingpb.GetContractReq{Id: 999_999_999}); err == nil {
		t.Error("不存在的合同应报错")
	}
}

// TestGetContract_NoDB 未配库时应明确失败(ErrDepConnect), 不能静默返回空合同.
func TestGetContract_NoDB(t *testing.T) {
	cli := startTestGRPC(t, &svc.ServiceContext{}) // DB = nil

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := cli.GetContract(ctx, &leasingpb.GetContractReq{Id: 1}); err == nil {
		t.Error("DB 未初始化时应失败, 而不是返回空合同")
	}
}

// TestGetOccupancy_EndToEnd 入驻率经 gRPC 返回, 且与 HTTP 同一实现(共用 logic).
func TestGetOccupancy_EndToEnd(t *testing.T) {
	db := openTestDB(t)
	cli := startTestGRPC(t, &svc.ServiceContext{DB: db})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := cli.GetOccupancy(ctx, &leasingpb.GetOccupancyReq{})
	if err != nil {
		t.Fatalf("GetOccupancy 失败: %v", err)
	}

	if resp.GetTotalAreaSqm() < 0 || resp.GetLeasedAreaSqm() < 0 {
		t.Errorf("面积不应为负: total=%v leased=%v", resp.GetTotalAreaSqm(), resp.GetLeasedAreaSqm())
	}
	// 不变量: 已租面积不可能大于可租总面积(可租为 0 时两者都是 0)
	if resp.GetTotalAreaSqm() > 0 && resp.GetLeasedAreaSqm() > resp.GetTotalAreaSqm() {
		t.Errorf("已租面积 %v 大于可租总面积 %v", resp.GetLeasedAreaSqm(), resp.GetTotalAreaSqm())
	}
	// 入驻率必须落在 0~1, 且总面积为 0 时不能是 NaN
	if resp.GetOccupancyRate() < 0 || resp.GetOccupancyRate() > 1 {
		t.Errorf("入驻率 = %v, 应在 0~1", resp.GetOccupancyRate())
	}
	if resp.GetTotalAreaSqm() == 0 && resp.GetOccupancyRate() != 0 {
		t.Errorf("可租总面积为 0 时入驻率应为 0, 实际 %v", resp.GetOccupancyRate())
	}
}

// TestExternalGRPC 对**已经独立启动的 grpcserver 进程**做真实调用.
//
// 为什么单独留这个用例: 前面的用例都是进程内起服务, 覆盖不到"启动配置本身"——
// 而 grpcserver/main.go + etc/leasing-grpc.yaml 的坑恰恰在于配置(xxx-grpc.yaml 少写
// Name 字段会直接导致 go-zero 拒绝启动, M1/M2 都踩过), 进程内测试永远发现不了。
//
// 默认跳过, 由外部显式指定地址后运行:
//
//	$env:M5_GRPC_ADDR="127.0.0.1:9051"; go test ./app/leasing-service/internal/rpcserver/ -run TestExternalGRPC -v
func TestExternalGRPC(t *testing.T) {
	addr := os.Getenv("M5_GRPC_ADDR")
	if addr == "" {
		t.Skip("跳过: 未设置 M5_GRPC_ADDR(该用例针对已启动的独立进程)")
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("拨号 %s 失败: %v", addr, err)
	}
	defer conn.Close()

	cli := leasingpb.NewLeasingServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ping 不依赖 DB, 能通就说明"服务起来了且协议可用"
	if _, err := cli.Ping(ctx, &commonpb.Empty{}); err != nil {
		t.Fatalf("对独立进程 Ping 失败: %v", err)
	}
	t.Logf("Ping 成功: addr=%s", addr)

	// 两个业务接口也真实调一次, 确认 DSN/DB 装配正确
	occ, err := cli.GetOccupancy(ctx, &leasingpb.GetOccupancyReq{})
	if err != nil {
		t.Fatalf("对独立进程 GetOccupancy 失败: %v", err)
	}
	t.Logf("GetOccupancy 成功: total=%v leased=%v rate=%v",
		occ.GetTotalAreaSqm(), occ.GetLeasedAreaSqm(), occ.GetOccupancyRate())
}

// TestToProtoContract_AllFieldsMapped 手写映射的兜底: 漏字段不会编译报错, 只能靠用例锁住.
func TestToProtoContract_AllFieldsMapped(t *testing.T) {
	dto := &types.Contract{
		Id: 7, ContractNo: "LC-7", TenantId: 3, TenantName: "租户A", ZoneCode: "A-1F-101",
		AreaSqm: 88.25, MonthlyRent: "8888.88", Deposit: "17777.76",
		StartDate: "2026-03-01", EndDate: "2027-02-28",
		Status: 2, AutoRenew: 1, RenewNoticeDays: 45,
	}

	got := toProtoContract(dto)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"id", got.GetId(), dto.Id},
		{"contract_no", got.GetContractNo(), dto.ContractNo},
		{"tenant_id", got.GetTenantId(), dto.TenantId},
		{"tenant_name", got.GetTenantName(), dto.TenantName},
		{"zone_code", got.GetZoneCode(), dto.ZoneCode},
		{"area_sqm", got.GetAreaSqm(), dto.AreaSqm},
		{"monthly_rent", got.GetMonthlyRent(), dto.MonthlyRent},
		{"deposit", got.GetDeposit(), dto.Deposit},
		{"start_date", got.GetStartDate(), dto.StartDate},
		{"end_date", got.GetEndDate(), dto.EndDate},
		{"status", got.GetStatus(), dto.Status},
		{"auto_renew", got.GetAutoRenew(), true},
		{"renew_notice_days", got.GetRenewNoticeDays(), dto.RenewNoticeDays},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, 期望 %v", c.name, c.got, c.want)
		}
	}

	// AutoRenew 是 int32(0/1) -> bool 的换算, 0 必须映射成 false
	dto.AutoRenew = 0
	if toProtoContract(dto).GetAutoRenew() {
		t.Error("auto_renew=0 应映射为 false")
	}
}
