package logic

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"onepark/app/shadow-service/internal/svc"
	"onepark/proto/shadow"

	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateDesiredLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateDesiredLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateDesiredLogic {
	return &UpdateDesiredLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// UpdateDesired 更新影子期望值, version 为乐观锁.
// version 传 0 表示不校验版本(按当前版本更新), 非 0 则必须匹配当前版本.
func (l *UpdateDesiredLogic) UpdateDesired(in *shadow.UpdateDesiredReq) (*shadow.UpdateShadowResp, error) {
	if in.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id 不能为空")
	}
	if len(in.GetDesired()) > 0 && !json.Valid(in.GetDesired()) {
		return nil, status.Error(codes.InvalidArgument, "desired 必须是合法 JSON")
	}

	desired := in.GetDesired()
	if len(desired) == 0 {
		desired = []byte("{}")
	}

	version, err := l.resolveVersion(in.GetDeviceId(), uint(in.GetVersion()))
	if err != nil {
		return nil, err
	}

	rows, err := l.svcCtx.ShadowModel.UpdateDesired(l.ctx, in.GetDeviceId(), desired, version)
	if err != nil {
		l.Errorf("更新期望值失败: %v", err)
		return nil, status.Error(codes.Internal, "更新期望值失败")
	}
	if rows == 0 {
		// 版本冲突: 返回 success=false, 由调用方决定重试
		return &shadow.UpdateShadowResp{
			DeviceId: in.GetDeviceId(),
			Success:  false,
			Message:  "版本冲突, 请重新获取影子后重试",
		}, nil
	}

	l.Infof("期望值更新成功: deviceId=%s, version=%d", in.GetDeviceId(), version+1)
	return &shadow.UpdateShadowResp{
		DeviceId: in.GetDeviceId(),
		Version:  uint32(version + 1),
		Success:  true,
	}, nil
}

// resolveVersion 确定乐观锁基准版本: 入参为 0 时取当前版本.
func (l *UpdateDesiredLogic) resolveVersion(deviceID string, want uint) (uint, error) {
	if want != 0 {
		return want, nil
	}
	cur, err := l.svcCtx.ShadowModel.FindByDeviceID(l.ctx, deviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, status.Error(codes.NotFound, "设备影子不存在")
		}
		l.Errorf("查询影子失败: %v", err)
		return 0, status.Error(codes.Internal, "查询影子失败")
	}
	return cur.Version, nil
}
