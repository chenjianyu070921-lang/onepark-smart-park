package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/access-control-service/internal/config"
	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"
	devicepb "onepark/proto/device"

	"google.golang.org/grpc"
)

// ---------------------------------------------------------------------------
// 测试替身: 远程开门链路不依赖真实 M1 与 MySQL
// ---------------------------------------------------------------------------

// fakeDeviceClient 复刻 M1 device-service 的 SendCommand 输出.
type fakeDeviceClient struct {
	res *commonpb.CommandResult
	// delay 用于在 ctx 超时后才返回, 模拟超时场景.
	delay time.Duration
	err   error
	// lastCommand 记录最后一次下发请求, 用于断言命令名与设备ID.
	lastCommand *commonpb.DeviceCommand
}

func (f *fakeDeviceClient) Ping(context.Context, *commonpb.Empty, ...grpc.CallOption) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

func (f *fakeDeviceClient) SendCommand(ctx context.Context, in *commonpb.DeviceCommand, _ ...grpc.CallOption) (*commonpb.CommandResult, error) {
	f.lastCommand = in
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.res, nil
}

func (f *fakeDeviceClient) GetDevice(context.Context, *devicepb.GetDeviceReq, ...grpc.CallOption) (*devicepb.GetDeviceResp, error) {
	return &devicepb.GetDeviceResp{}, nil
}

// GetDeviceStat 仅为满足接口: 远程开门链路不使用它.
// 注意: 该方法由 M1 为 M5 大屏新增(proto/device), 本文件此前未同步实现,
// 因此 access-control 的全部单测在 HEAD 上是编译失败的 —— 此处补齐.
func (f *fakeDeviceClient) GetDeviceStat(context.Context, *devicepb.GetDeviceStatReq, ...grpc.CallOption) (*devicepb.GetDeviceStatResp, error) {
	return &devicepb.GetDeviceStatResp{}, nil
}

var _ devicepb.DeviceServiceClient = (*fakeDeviceClient)(nil)

// fakeOperateLogStore 捕获写入的审计记录.
type fakeOperateLogStore struct {
	logs []*model.AccessOperateLog
	err  error
}

func (f *fakeOperateLogStore) Create(_ context.Context, log *model.AccessOperateLog) error {
	if f.err != nil {
		return f.err
	}
	f.logs = append(f.logs, log)
	return nil
}

var _ model.OperateLogModel = (*fakeOperateLogStore)(nil)

// allowedDevice 单测中默认允许的设备, 与 etc/accesscontrol-api.yaml 的默认白名单对齐.
const allowedDevice = "door-01"

// newTestContext 构造带 M1 客户端与审计存储的上下文, 远程开门白名单默认放行 allowedDevice.
func newTestContext(client devicepb.DeviceServiceClient, store model.OperateLogModel) *svc.ServiceContext {
	return &svc.ServiceContext{
		Config:      config.Config{RemoteOpen: config.RemoteOpenConf{AllowedDeviceIds: []string{allowedDevice}}},
		DeviceRPC:   client,
		OperateLogs: store,
	}
}

// newTenantCtx 构造带租户与操作人的 context(与 middleware.ContextMiddleware 注入语义一致).
func newTenantCtx(tenantID, userID int64) context.Context {
	ctx := ctxdata.SetTenantId(context.Background(), tenantID)
	return ctxdata.SetUserId(ctx, userID)
}

// ---------------------------------------------------------------------------
// 验收用例
// ---------------------------------------------------------------------------

// TestRemoteOpen_Success 成功下发: 返回 success=true, 且无论成败都留下 result=1 的审计.
func TestRemoteOpen_Success(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true, Message: "ok"}}
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	resp, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01", Reason: "访客放行"})
	if err != nil {
		t.Fatalf("开门应成功: %v", err)
	}
	if !resp.Success || resp.DeviceId != "door-01" {
		t.Errorf("响应异常: %+v", resp)
	}

	// 命令名必须是 M1 约定的 open_door.
	if client.lastCommand == nil || client.lastCommand.Command != commandOpenDoor {
		t.Errorf("下发的命令名异常: %+v", client.lastCommand)
	}

	if len(store.logs) != 1 {
		t.Fatalf("应留下 1 条审计, 实际 %d 条", len(store.logs))
	}
	got := store.logs[0]
	if got.Result != model.CommandResultSuccess || got.OperatorID != 9 || got.TenantID != 1 {
		t.Errorf("审计内容异常: %+v", got)
	}
	if got.Reason != "访客放行" || got.Command != commandOpenDoor {
		t.Errorf("审计缘由/命令异常: reason=%s command=%s", got.Reason, got.Command)
	}
}

// TestRemoteOpen_M1FailureAuditAndError M1 返回失败: 对外报错 M3-E-2003, 审计仍记录 result=0.
func TestRemoteOpen_M1FailureAuditAndError(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: false, Message: "设备离线"}}
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	_, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"})
	if err == nil {
		t.Fatal("M1 返回失败时应报错")
	}
	ce, ok := err.(*errorx.CodeError)
	if !ok {
		t.Fatalf("错误类型应为 CodeError, 实际 %T", err)
	}
	if ce.Code != errorx.ErrAccessRemoteOpen {
		t.Errorf("错误码应为 %s, 实际 %s", errorx.ErrAccessRemoteOpen, ce.Code)
	}

	if len(store.logs) != 1 || store.logs[0].Result != model.CommandResultFail {
		t.Fatalf("失败应留下 result=0 的审计: %+v", store.logs)
	}
	if store.logs[0].Message != "设备离线" {
		t.Errorf("审计应记录 M1 返回的原因: %s", store.logs[0].Message)
	}
}

// TestRemoteOpen_Timeout M1 超时: 错误码不变, 审计标记为 result=2(超时).
func TestRemoteOpen_Timeout(t *testing.T) {
	client := &fakeDeviceClient{delay: 2 * time.Second}
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	_, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"})
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if len(store.logs) != 1 || store.logs[0].Result != model.CommandResultTimeout {
		t.Fatalf("超时审计应标记为 2: %+v", store.logs)
	}
}

// TestRemoteOpen_TransportError gRPC 传输错误(非超时)按失败记录审计.
func TestRemoteOpen_TransportError(t *testing.T) {
	client := &fakeDeviceClient{err: errors.New("connection refused")}
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	if _, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"}); err == nil {
		t.Fatal("传输错误应返回错误")
	}
	if len(store.logs) != 1 || store.logs[0].Result != model.CommandResultFail {
		t.Fatalf("传输错误应记录为失败: %+v", store.logs)
	}
}

// TestRemoteOpen_ParamInvalid 参数非法: 返回 W 级错误码(HTTP 400), 且不产生审计.
func TestRemoteOpen_ParamInvalid(t *testing.T) {
	cases := map[string]struct {
		tenantID int64
		userID   int64
		deviceID string
	}{
		"缺少 device_id": {tenantID: 1, userID: 9, deviceID: ""},
		"缺少租户":         {tenantID: 0, userID: 9, deviceID: "door-01"},
		"缺少操作人":        {tenantID: 1, userID: 0, deviceID: "door-01"},
	}

	for name, c := range cases {
		store := &fakeOperateLogStore{}
		client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: true}}
		logic := NewRemoteOpenLogic(newTenantCtx(c.tenantID, c.userID), newTestContext(client, store))

		_, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: c.deviceID})
		if err == nil {
			t.Fatalf("%s: 应返回参数错误", name)
		}
		ce, ok := err.(*errorx.CodeError)
		if !ok || ce.Code != errorx.ErrAccessParamInvalid {
			t.Errorf("%s: 期望 %s, 实际 %v", name, errorx.ErrAccessParamInvalid, err)
		}
		if len(store.logs) != 0 {
			t.Errorf("%s: 参数非法不应写审计, 实际 %d 条", name, len(store.logs))
		}
	}
}

// TestRemoteOpen_DeviceRPCMissing M1 客户端未配置: 返回依赖错误, 不下发也不写审计.
func TestRemoteOpen_DeviceRPCMissing(t *testing.T) {
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(nil, store))

	_, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrDepConnect {
		t.Fatalf("期望依赖错误 %s, 实际 %v", errorx.ErrDepConnect, err)
	}
	if len(store.logs) != 0 {
		t.Errorf("M1 未配置不应写审计, 实际 %d 条", len(store.logs))
	}
}

// TestRemoteOpen_AuditFailureNotMaskResult 审计写入失败不能把成功的开门伪装成失败.
func TestRemoteOpen_AuditFailureNotMaskResult(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: true}}
	store := &fakeOperateLogStore{err: errors.New("mysql down")}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	resp, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"})
	if err != nil {
		t.Fatalf("审计失败不应影响开门结果: %v", err)
	}
	if !resp.Success {
		t.Error("审计失败时仍应返回成功")
	}
}

// TestRemoteOpen_DeniedByAllowlist 白名单外设备拒绝开门: 返回无权限错误, 且不向 M1 下发命令, 但必须留审计.
func TestRemoteOpen_DeniedByAllowlist(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: true}}
	store := &fakeOperateLogStore{}
	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), newTestContext(client, store))

	_, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-99"})
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrForbidden {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrForbidden, err)
	}
	// 拒绝时绝不能把命令下发到 M1.
	if client.lastCommand != nil {
		t.Errorf("被拒绝的请求仍下发了命令: %+v", client.lastCommand)
	}
	// 未授权尝试必须留痕(安全事件可追溯).
	if len(store.logs) != 1 || store.logs[0].Result != model.CommandResultFail {
		t.Fatalf("拒绝尝试应留下 result=0 的审计: %+v", store.logs)
	}
	if store.logs[0].DeviceID != "door-99" || store.logs[0].OperatorID != 9 {
		t.Errorf("审计应记录设备与操作人: %+v", store.logs[0])
	}
}

// TestRemoteOpen_EmptyAllowlistFailClosed 白名单为空时拒绝所有设备(fail-closed, 防配置遗漏).
func TestRemoteOpen_EmptyAllowlistFailClosed(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: true}}
	store := &fakeOperateLogStore{}
	svcCtx := newTestContext(client, store)
	svcCtx.Config.RemoteOpen.AllowedDeviceIds = nil

	logic := NewRemoteOpenLogic(newTenantCtx(1, 9), svcCtx)
	if _, err := logic.RemoteOpen(&types.RemoteOpenReq{DeviceId: "door-01"}); err == nil {
		t.Fatal("白名单为空应拒绝所有远程开门")
	}
	if client.lastCommand != nil {
		t.Error("未配置白名单时不应下发命令")
	}
}

// TestIsTimeout 超时判定的分支覆盖: nil / 本地上下文超时 / gRPC DeadlineExceeded.
func TestIsTimeout(t *testing.T) {
	if isTimeout(nil) {
		t.Error("nil 不应判定为超时")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if !isTimeout(ctx.Err()) {
		t.Error("context.DeadlineExceeded 应判定为超时")
	}
	if isTimeout(errors.New("boom")) {
		t.Error("普通错误不应判定为超时")
	}
}
