package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"
)

// openReq 构造远程开门请求: requestID 为空表示调用方未携带幂等键.
func openReq(deviceID, requestID string) *types.RemoteOpenReq {
	return &types.RemoteOpenReq{DeviceId: deviceID, RequestId: requestID}
}

// fakeDeduper 内存幂等替身, 记录被判定过的键与被释放的键.
type fakeDeduper struct {
	seen    map[string]bool
	keys    []string
	released []string
	// releaseErr 模拟"清键时 Redis 抖动".
	releaseErr error
	failErr    error
}

func (f *fakeDeduper) Seen(_ context.Context, key string) (bool, error) {
	if f.failErr != nil {
		return false, f.failErr
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.keys = append(f.keys, key)
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

func (f *fakeDeduper) Release(_ context.Context, key string) error {
	if f.releaseErr != nil {
		return f.releaseErr
	}
	f.released = append(f.released, key)
	delete(f.seen, key)
	return nil
}

func newRemoteOpenCtx(client *fakeDeviceClient, logs *fakeOperateLogStore,
	perms *fakePermissionStore, records *fakeRecordStore, dedup *fakeDeduper) *RemoteOpenLogic {
	svcCtx := newTestContext(client, logs)
	// 只赋值非 nil 的替身: 直接赋 (*T)(nil) 会得到一个"非 nil 接口 + nil 指针",
	// 调用其方法必然 panic —— 与生产环境"未初始化即 nil 接口"的语义不符.
	if perms != nil {
		svcCtx.Permissions = perms
	}
	if records != nil {
		svcCtx.Records = records
	}
	if dedup != nil {
		svcCtx.Dedup = dedup
	}
	return NewRemoteOpenLogic(newTenantCtx(1, 9), svcCtx)
}

// ---------------------------------------------------------------------------
// 幂等: 同一 request_id 只下发一次开门命令
// ---------------------------------------------------------------------------

// TestRemoteOpen_Idempotent 重复提交必须只产生一次真实的开门下发.
// 门被多开一次是不可逆的物理动作, 重复提交必须挡在命令下发之前.
func TestRemoteOpen_Idempotent(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true, Message: "ok"}}
	logs := &fakeOperateLogStore{}
	records := &fakeRecordStore{}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, logs, nil, records, d)

	first, err := l.RemoteOpen(openReq("door-01", "req-1"))
	if err != nil {
		t.Fatalf("首次开门应成功: %v", err)
	}
	if !first.Success {
		t.Fatalf("首次应返回成功: %+v", first)
	}
	if client.lastCommand == nil {
		t.Fatal("首次应下发开门命令")
	}

	// 重置下发记录以确认第二次没有再下发.
	client.lastCommand = nil
	second, err := l.RemoteOpen(openReq("door-01", "req-1"))
	if err != nil {
		t.Fatalf("重复请求不应报错(幂等应静默返回): %v", err)
	}
	if client.lastCommand != nil {
		t.Errorf("重复 request_id 不应重复下发开门命令: %+v", client.lastCommand)
	}
	if len(logs.logs) != 1 {
		t.Errorf("重复请求不应重复写审计, 实际 %d 条", len(logs.logs))
	}
	if len(records.created) != 1 {
		t.Errorf("重复请求不应重复写通行记录, 实际 %d 条", len(records.created))
	}
	_ = second
}

// TestRemoteOpen_DifferentRequestIdAllowed 不同 request_id 是两次真实开门, 不能被幂等误杀.
func TestRemoteOpen_DifferentRequestIdAllowed(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true}}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	if _, err := l.RemoteOpen(openReq("door-01", "req-A")); err != nil {
		t.Fatalf("第一次开门应成功: %v", err)
	}
	client.lastCommand = nil
	if _, err := l.RemoteOpen(openReq("door-01", "req-B")); err != nil {
		t.Fatalf("不同 request_id 应允许再次开门: %v", err)
	}
	if client.lastCommand == nil {
		t.Error("不同 request_id 应真实下发命令")
	}
}

// TestRemoteOpen_NoRequestIdSkipsDedup 不带 request_id 的老调用方仍需正常工作.
func TestRemoteOpen_NoRequestIdSkipsDedup(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true}}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	for i := 0; i < 2; i++ {
		client.lastCommand = nil
		if _, err := l.RemoteOpen(openReq("door-01", "")); err != nil {
			t.Fatalf("第 %d 次开门应成功: %v", i+1, err)
		}
		if client.lastCommand == nil {
			t.Errorf("第 %d 次应下发命令", i+1)
		}
	}
	if len(d.keys) != 0 {
		t.Errorf("无 request_id 时不应查询幂等键: %v", d.keys)
	}
}

// TestRemoteOpen_DedupUnavailableFailClosed 幂等组件不可用时拒绝开门:
// 无法确认"是否重复"时, 宁可开不了也不能重复开.
func TestRemoteOpen_DedupUnavailableFailClosed(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true}}
	d := &fakeDeduper{failErr: errors.New("redis down")}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	_, err := l.RemoteOpen(openReq("door-01", "req-1"))
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAccessRemoteOpen {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrAccessRemoteOpen, err)
	}
	if client.lastCommand != nil {
		t.Error("幂等不可用时不应下发命令")
	}
}

// ---------------------------------------------------------------------------
// 幂等键的释放: 失败必须可重试, 且重试不能拿到"假成功"
//
// 背景: 幂等键是"命令下发前"预占的(为挡住前端连点造成的重复开门). 若不在动作未成功时释放,
// 客户端带同一 request_id 重试会直接命中幂等分支返回 {success:true}, 而门一次都没开 ——
// 对门禁来说"以为开了其实没开"与"重复开"是同一量级的危险, 且前者更难被发现.
// ---------------------------------------------------------------------------

// TestRemoteOpen_M1FailureAllowsRetryWithSameRequestId 下发失败后, 同一 request_id 重试必须真正重新下发.
func TestRemoteOpen_M1FailureAllowsRetryWithSameRequestId(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: false, Message: "设备离线"}}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	if _, err := l.RemoteOpen(openReq("door-01", "req-retry")); err == nil {
		t.Fatal("M1 返回失败时应报错")
	}
	if len(d.released) != 1 {
		t.Fatalf("下发失败后必须释放幂等键(否则重试会拿到假成功), released=%v", d.released)
	}

	// M1 恢复后带同一 request_id 重试.
	client.lastCommand = nil
	client.res = &commonpb.CommandResult{DeviceId: "door-01", Success: true, Message: "ok"}
	resp, err := l.RemoteOpen(openReq("door-01", "req-retry"))
	if err != nil {
		t.Fatalf("M1 恢复后同一 request_id 重试应真正开门: %v", err)
	}
	if !resp.Success {
		t.Errorf("重试成功应返回 success=true: %+v", resp)
	}
	if client.lastCommand == nil {
		t.Error("重试必须重新下发开门命令: 被幂等分支挡掉会返回'假成功'而门根本没开")
	}
}

// TestRemoteOpen_TimeoutReleasesKeyForRetry 超时与失败同样要释放: 超时是最常见的"其实没开成"。
func TestRemoteOpen_TimeoutReleasesKeyForRetry(t *testing.T) {
	client := &fakeDeviceClient{delay: 2 * time.Second}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	if _, err := l.RemoteOpen(openReq("door-01", "req-timeout")); err == nil {
		t.Fatal("M1 超时应返回错误")
	}
	if len(d.released) != 1 {
		t.Errorf("超时同样必须释放幂等键, released=%v", d.released)
	}
}

// TestRemoteOpen_DeniedReleasesKeySoRetryIsNotFakeSuccess 被拒时一条命令都没下发, 重试不得变成假成功.
func TestRemoteOpen_DeniedReleasesKeySoRetryIsNotFakeSuccess(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: true}}
	d := &fakeDeduper{}
	// 白名单只含 door-01, 故 door-99 必被拒.
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	_, err := l.RemoteOpen(openReq("door-99", "req-denied"))
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrForbidden {
		t.Fatalf("期望 %s, 实际 %v", errorx.ErrForbidden, err)
	}
	if len(d.released) != 1 {
		t.Fatalf("被拒时必须释放幂等键(未下发任何命令), released=%v", d.released)
	}

	// 同一 request_id 重试: 必须仍是被拒, 而不是命中幂等返回 success=true.
	_, err = l.RemoteOpen(openReq("door-99", "req-denied"))
	ce, ok = err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrForbidden {
		t.Fatalf("被拒后重试应仍返回 %s(而非假成功), 实际 %v", errorx.ErrForbidden, err)
	}
	if client.lastCommand != nil {
		t.Error("被拒的请求任何一次都不应下发命令")
	}
}

// TestRemoteOpen_SuccessKeepsKeyBlockingDuplicate 成功开门后**不得**释放键:
// 键的存续正是"该 request_id 已真实开过一次门"的凭证, 释放会让前端连点变成两次真实开门.
func TestRemoteOpen_SuccessKeepsKeyBlockingDuplicate(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-01", Success: true, Message: "ok"}}
	d := &fakeDeduper{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	if _, err := l.RemoteOpen(openReq("door-01", "req-ok")); err != nil {
		t.Fatalf("首次开门应成功: %v", err)
	}
	if len(d.released) != 0 {
		t.Fatalf("成功开门后不得释放幂等键, released=%v", d.released)
	}

	client.lastCommand = nil
	if _, err := l.RemoteOpen(openReq("door-01", "req-ok")); err != nil {
		t.Fatalf("重复请求应静默返回: %v", err)
	}
	if client.lastCommand != nil {
		t.Error("成功后重复 request_id 必须被幂等挡住, 不得再次下发开门命令")
	}
}

// TestRemoteOpen_ReleaseFailureDoesNotMaskOutcome 清键失败只记日志, 不得改写开门结果.
func TestRemoteOpen_ReleaseFailureDoesNotMaskOutcome(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: false, Message: "设备离线"}}
	d := &fakeDeduper{releaseErr: errors.New("redis down")}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, &fakeRecordStore{}, d)

	_, err := l.RemoteOpen(openReq("door-01", "req-1"))
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAccessRemoteOpen {
		t.Fatalf("清键失败不得改写开门结果, 期望 %s, 实际 %v", errorx.ErrAccessRemoteOpen, err)
	}
}

// ---------------------------------------------------------------------------
// 授权真正生效
// ---------------------------------------------------------------------------

// TestRemoteOpen_GrantedPermissionBypassesAllowlist 被授权的人可以开不在白名单内的门.
// 这是"授权落地"的核心: access_permission 从此产生约束力, 不再是一张没人读的表.
func TestRemoteOpen_GrantedPermissionBypassesAllowlist(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: "door-77", Success: true}}
	perms := &fakePermissionStore{effective: &model.AccessPermission{
		PersonID: 9, DeviceID: "door-77", Status: model.PermissionStatusValid, Whitelist: 1,
	}}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, perms, &fakeRecordStore{}, nil)

	if _, err := l.RemoteOpen(openReq("door-77", "")); err != nil {
		t.Fatalf("有有效授权时应放行: %v", err)
	}
	if client.lastCommand == nil {
		t.Error("放行后应下发命令")
	}
}

// TestRemoteOpen_ExpiredPermissionDenied 授权已过期时即使设备在白名单内也要拒绝:
// 授权是比白名单更细的控制, 不能被白名单盖掉.
func TestRemoteOpen_ExpiredPermissionDenied(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: allowedDevice, Success: true}}
	expired := time.Now().Add(-time.Hour)
	perms := &fakePermissionStore{effective: &model.AccessPermission{
		PersonID: 9, DeviceID: allowedDevice, Status: model.PermissionStatusValid, ExpireAt: &expired,
	}}
	logs := &fakeOperateLogStore{}
	l := newRemoteOpenCtx(client, logs, perms, &fakeRecordStore{}, nil)

	_, err := l.RemoteOpen(openReq(allowedDevice, ""))
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrForbidden {
		t.Fatalf("过期授权应拒绝, 期望 %s, 实际 %v", errorx.ErrForbidden, err)
	}
	if client.lastCommand != nil {
		t.Error("被拒绝的请求不应下发命令")
	}
	if len(logs.logs) != 1 || logs.logs[0].Result != model.CommandResultFail {
		t.Errorf("拒绝应留下失败审计: %+v", logs.logs)
	}
}

// TestRemoteOpen_PermissionStoreErrorFallsBackToAllowlist 权限库故障时回退白名单,
// 但必须留日志(这里以"仍放行"体现白名单兜底未失效).
func TestRemoteOpen_PermissionStoreErrorFallsBackToAllowlist(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: allowedDevice, Success: true}}
	perms := &fakePermissionStore{err: errors.New("mysql down")}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, perms, &fakeRecordStore{}, nil)

	if _, err := l.RemoteOpen(openReq(allowedDevice, "")); err != nil {
		t.Fatalf("权限库故障时白名单仍应放行: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 通行记录写入(#48 的数据来源)
// ---------------------------------------------------------------------------

// TestRemoteOpen_WritesAccessRecord 成功开门写入 result=1 的通行记录.
func TestRemoteOpen_WritesAccessRecord(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{DeviceId: allowedDevice, Success: true}}
	records := &fakeRecordStore{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, records, nil)

	if _, err := l.RemoteOpen(openReq(allowedDevice, "")); err != nil {
		t.Fatalf("开门应成功: %v", err)
	}
	if len(records.created) != 1 {
		t.Fatalf("应写 1 条通行记录, 实际 %d", len(records.created))
	}
	got := records.created[0]
	if got.Result != model.AccessResultSuccess || got.OpenType != model.OpenTypeRemote {
		t.Errorf("通行记录内容异常: %+v", got)
	}
	if got.PersonID != 9 || got.TenantID != 1 || got.DeviceID != allowedDevice {
		t.Errorf("通行记录未按租户+操作人+设备记录: %+v", got)
	}
}

// TestRemoteOpen_WritesFailureRecord 开门失败写入 result=0 且带失败原因.
func TestRemoteOpen_WritesFailureRecord(t *testing.T) {
	client := &fakeDeviceClient{res: &commonpb.CommandResult{Success: false, Message: "设备离线"}}
	records := &fakeRecordStore{}
	l := newRemoteOpenCtx(client, &fakeOperateLogStore{}, nil, records, nil)

	if _, err := l.RemoteOpen(openReq(allowedDevice, "")); err == nil {
		t.Fatal("失败应返回错误")
	}
	if len(records.created) != 1 {
		t.Fatalf("失败也应留通行记录, 实际 %d", len(records.created))
	}
	got := records.created[0]
	if got.Result != model.AccessResultFail {
		t.Errorf("失败记录 result 应为 0: %+v", got)
	}
	if got.FailReason != "设备离线" {
		t.Errorf("失败原因未记录: %q", got.FailReason)
	}
}
