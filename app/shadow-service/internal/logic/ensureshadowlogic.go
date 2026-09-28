package logic

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"onepark/app/shadow-service/internal/model"
	"onepark/app/shadow-service/internal/svc"
	"onepark/proto/shadow"

	"github.com/zeromicro/go-zero/core/logx"
)

type EnsureShadowLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewEnsureShadowLogic(ctx context.Context, svcCtx *svc.ServiceContext) *EnsureShadowLogic {
	return &EnsureShadowLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// EnsureShadow 确保设备影子存在(幂等): 不存在则以空 desired/reported、version=0 创建;
// 已存在则直接返回现状(created=false), 调用方无需区分.
// 设备注册成功后由 device-service 调用, 影子创建从"遥测惰性补建"变为"注册显式创建".
func (l *EnsureShadowLogic) EnsureShadow(in *shadow.EnsureShadowReq) (*shadow.EnsureShadowResp, error) {
	if in.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id 不能为空")
	}

	cur, err := l.svcCtx.ShadowModel.FindByDeviceID(l.ctx, in.GetDeviceId())
	if err == nil {
		return &shadow.EnsureShadowResp{
			DeviceId: in.GetDeviceId(),
			Version:  uint32(cur.Version),
			Created:  false,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		l.Errorf("查询影子失败: %v", err)
		return nil, status.Error(codes.Internal, "查询影子失败")
	}

	s := &model.Shadow{
		DeviceID: in.GetDeviceId(),
		Desired:  datatypes.JSON([]byte(`{}`)),
		Reported: datatypes.JSON([]byte(`{}`)),
		Version:  0,
	}
	if err := l.svcCtx.ShadowModel.Insert(l.ctx, s); err != nil {
		// 并发注册同一设备时唯一索引兜底: 转为已存在语义, 保持幂等
		cur, qerr := l.svcCtx.ShadowModel.FindByDeviceID(l.ctx, in.GetDeviceId())
		if qerr == nil {
			return &shadow.EnsureShadowResp{
				DeviceId: in.GetDeviceId(),
				Version:  uint32(cur.Version),
				Created:  false,
			}, nil
		}
		l.Errorf("影子创建失败: %v", err)
		return nil, status.Error(codes.Internal, "影子创建失败")
	}

	l.Infof("影子创建成功: deviceId=%s", in.GetDeviceId())
	return &shadow.EnsureShadowResp{
		DeviceId: in.GetDeviceId(),
		Version:  0,
		Created:  true,
	}, nil
}
