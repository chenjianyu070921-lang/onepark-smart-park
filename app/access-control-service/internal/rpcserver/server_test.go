package rpcserver

import (
	"context"
	"testing"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	accesspb "onepark/proto/access"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakePermissions 权限存储替身: 只实现 gRPC 用到的 FindEffective.
type fakePermissions struct {
	perm *model.AccessPermission
	err  error
	got  struct {
		tenantID int64
		personID int64
		deviceID string
	}
}

func (f *fakePermissions) FindEffective(_ context.Context, tenantID, personID int64, deviceID string) (*model.AccessPermission, error) {
	f.got.tenantID, f.got.personID, f.got.deviceID = tenantID, personID, deviceID
	return f.perm, f.err
}

func (f *fakePermissions) Grant(context.Context, []*model.AccessPermission) (int64, error) { return 0, nil }

func (f *fakePermissions) Revoke(context.Context, int64, []int64, []string) (int64, error) {
	return 0, nil
}

var _ model.PermissionModel = (*fakePermissions)(nil)

// timeWindow 构造库内的时间段 JSON.
func timeWindow(start, end string, days ...int64) *string {
	s := `{"start":"` + start + `","end":"` + end + `","days":[`
	for i, d := range days {
		if i > 0 {
			s += ","
		}
		s += string(rune('0' + d))
	}
	s += `]}`
	return &s
}

// TestCheckPermission_AllowedInsideWindow 时间段内放行, 且判定时刻取请求里的 at(可确定性验证).
//
// at 必须由请求传入而不是取 time.Now(): 取当前时刻的用例会在夜里失败, 是典型的"只在白天绿"。
func TestCheckPermission_AllowedInsideWindow(t *testing.T) {
	store := &fakePermissions{perm: &model.AccessPermission{
		Status:     model.PermissionStatusValid,
		TimeWindow: timeWindow("08:00", "20:00", 1, 2, 3, 4, 5),
	}}
	s := NewAccessServer(&svc.ServiceContext{Permissions: store})

	// 2026-09-22 是周二(day=2), 10:00 落在窗口内.
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	resp, err := s.CheckPermission(context.Background(), &accesspb.CheckPermissionReq{
		TenantId: 7, PersonId: 100, DeviceId: "door-01", At: at.Unix(),
	})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if !resp.Allowed {
		t.Errorf("工作日 10:00 应放行, 实际拒绝: %s", resp.Reason)
	}
	if store.got.tenantID != 7 || store.got.personID != 100 || store.got.deviceID != "door-01" {
		t.Errorf("查询条件未透传: %+v", store.got)
	}
}

// TestCheckPermission_DeniedOutsideWindow 同一份授权, 换到窗口外的时刻必须拒绝 ——
// 放行与拒绝是同一条判定路径的两个分支, 只测放行等于没测时间段。
func TestCheckPermission_DeniedOutsideWindow(t *testing.T) {
	store := &fakePermissions{perm: &model.AccessPermission{
		Status:     model.PermissionStatusValid,
		TimeWindow: timeWindow("08:00", "20:00", 1, 2, 3, 4, 5),
	}}
	s := NewAccessServer(&svc.ServiceContext{Permissions: store})

	// 同一天的 22:00: 超出 20:00 上限.
	at := time.Date(2026, 9, 22, 22, 0, 0, 0, time.Local)
	resp, err := s.CheckPermission(context.Background(), &accesspb.CheckPermissionReq{
		TenantId: 7, PersonId: 100, DeviceId: "door-01", At: at.Unix(),
	})
	if err != nil {
		t.Fatalf("查询应成功: %v", err)
	}
	if resp.Allowed {
		t.Fatal("22:00 超出授权时间段, 不应放行")
	}
	if resp.Reason == "" {
		t.Error("拒绝时必须给出原因, 否则调用方与审计都不知道为什么被拦")
	}
}

// TestCheckPermission_NoRecordIsDenied 查不到授权 = 不放行(fail-closed), 且不算错误.
func TestCheckPermission_NoRecordIsDenied(t *testing.T) {
	s := NewAccessServer(&svc.ServiceContext{Permissions: &fakePermissions{}})
	resp, err := s.CheckPermission(context.Background(), &accesspb.CheckPermissionReq{
		TenantId: 7, PersonId: 100, DeviceId: "door-01",
	})
	if err != nil {
		t.Fatalf("无授权记录是正常结果, 不应返回错误: %v", err)
	}
	if resp.Allowed {
		t.Error("无授权记录必须拒绝: 门禁的默认值是不放行")
	}
}

// TestCheckPermission_StorageUnavailableFailsClosed 存储不可用必须拒绝而不是放行.
// 这是本接口最危险的一类退化: 查不到就放行等于把一次 DB 故障变成一栋楼的通行权限.
func TestCheckPermission_StorageUnavailableFailsClosed(t *testing.T) {
	s := NewAccessServer(&svc.ServiceContext{}) // Permissions 为 nil
	_, err := s.CheckPermission(context.Background(), &accesspb.CheckPermissionReq{
		TenantId: 7, PersonId: 100, DeviceId: "door-01",
	})
	if err == nil {
		t.Fatal("权限存储未就绪时必须返回错误, 不能放行")
	}
	if status.Code(err) != codes.Unavailable {
		t.Errorf("应返回 Unavailable, 实际 %v", status.Code(err))
	}
}

// TestCheckPermission_MissingParams 缺参数必须 InvalidArgument, 且不触达存储层.
func TestCheckPermission_MissingParams(t *testing.T) {
	store := &fakePermissions{}
	s := NewAccessServer(&svc.ServiceContext{Permissions: store})

	cases := map[string]*accesspb.CheckPermissionReq{
		"缺租户":   {PersonId: 100, DeviceId: "door-01"},
		"缺人员":   {TenantId: 7, DeviceId: "door-01"},
		"缺设备":   {TenantId: 7, PersonId: 100},
		"设备为空白": {TenantId: 7, PersonId: 100, DeviceId: "   "},
	}
	for name, req := range cases {
		if _, err := s.CheckPermission(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: 应返回 InvalidArgument, 实际 %v", name, status.Code(err))
		}
	}
	if store.got.deviceID != "" {
		t.Error("参数校验失败时不应触达存储层")
	}
}

// TestRemoteOpen_MissingTenantRejected gRPC 侧没有网关注入身份, 缺租户必须被拒绝.
// 否则一次不带 tenant_id 的调用会走成"匿名开门"。
func TestRemoteOpen_MissingTenantRejected(t *testing.T) {
	s := NewAccessServer(&svc.ServiceContext{})
	_, err := s.RemoteOpen(context.Background(), &accesspb.RemoteOpenReq{
		DeviceId: "door-01", OperatorId: 9,
	})
	if err == nil {
		t.Fatal("缺少 tenant_id 时必须拒绝开门")
	}
}

// TestRemoteOpen_DeviceRPCUnavailable 未配置 M1 时开门报错而非静默成功:
// 调用方必须能区分"门开了"与"命令没发出去"。
func TestRemoteOpen_DeviceRPCUnavailable(t *testing.T) {
	s := NewAccessServer(&svc.ServiceContext{}) // DeviceRPC 为 nil
	_, err := s.RemoteOpen(context.Background(), &accesspb.RemoteOpenReq{
		TenantId: 7, DeviceId: "door-01", OperatorId: 9,
	})
	if err == nil {
		t.Fatal("M1 设备服务未配置时应返回错误, 不能假成功(见 KI-19 同类问题)")
	}
}

// TestPing 探活.
func TestPing(t *testing.T) {
	if _, err := NewAccessServer(&svc.ServiceContext{}).Ping(context.Background(), nil); err != nil {
		t.Errorf("Ping 不应报错: %v", err)
	}
}
