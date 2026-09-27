package logic

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"onepark/app/shadow-service/internal/model"
	"onepark/app/shadow-service/internal/svc"
	"onepark/proto/shadow"
)

// fakeShadowModel 内存版 ShadowModel, 验证 EnsureShadow 的幂等语义.
type fakeShadowModel struct {
	store   map[string]*model.Shadow
	findErr error
}

func newFakeShadowModel() *fakeShadowModel {
	return &fakeShadowModel{store: map[string]*model.Shadow{}}
}

func (f *fakeShadowModel) Insert(_ context.Context, s *model.Shadow) error {
	if _, ok := f.store[s.DeviceID]; ok {
		return errors.New("Error 1062: Duplicate entry")
	}
	cp := *s
	f.store[s.DeviceID] = &cp
	return nil
}

func (f *fakeShadowModel) FindByDeviceID(_ context.Context, deviceID string) (*model.Shadow, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	if s, ok := f.store[deviceID]; ok {
		return s, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeShadowModel) UpdateDesired(_ context.Context, _ string, _ []byte, _ uint) (int64, error) {
	return 0, nil
}

func (f *fakeShadowModel) UpdateReported(_ context.Context, _ string, _ []byte, _ uint) (int64, error) {
	return 0, nil
}

func (f *fakeShadowModel) Delete(_ context.Context, _ string) error { return nil }

func TestEnsureShadowEmptyDeviceID(t *testing.T) {
	l := NewEnsureShadowLogic(context.Background(), &svc.ServiceContext{ShadowModel: newFakeShadowModel()})
	_, err := l.EnsureShadow(&shadow.EnsureShadowReq{DeviceId: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("期望 InvalidArgument, 实际: %v", err)
	}
}

func TestEnsureShadowCreateThenIdempotent(t *testing.T) {
	m := newFakeShadowModel()
	l := NewEnsureShadowLogic(context.Background(), &svc.ServiceContext{ShadowModel: m})

	resp, err := l.EnsureShadow(&shadow.EnsureShadowReq{DeviceId: "dev-1"})
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if !resp.GetCreated() || resp.GetVersion() != 0 {
		t.Fatalf("首次创建应 created=true, version=0, 实际: %+v", resp)
	}

	// 模拟版本已被 UpdateDesired 推进后, 重复 Ensure 不得覆盖
	m.store["dev-1"].Version = 3
	resp2, err := l.EnsureShadow(&shadow.EnsureShadowReq{DeviceId: "dev-1"})
	if err != nil {
		t.Fatalf("重复调用失败: %v", err)
	}
	if resp2.GetCreated() || resp2.GetVersion() != 3 {
		t.Fatalf("重复调用应 created=false 且返回现状 version=3, 实际: %+v", resp2)
	}
}

func TestEnsureShadowQueryError(t *testing.T) {
	m := newFakeShadowModel()
	m.findErr = errors.New("connection refused")
	l := NewEnsureShadowLogic(context.Background(), &svc.ServiceContext{ShadowModel: m})
	_, err := l.EnsureShadow(&shadow.EnsureShadowReq{DeviceId: "dev-1"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("期望 Internal, 实际: %v", err)
	}
}
