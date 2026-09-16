package logic

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"onepark/app/shadow-service/internal/svc"
	"onepark/proto/shadow"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetShadowLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetShadowLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetShadowLogic {
	return &GetShadowLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetShadowLogic) GetShadow(in *shadow.GetShadowReq) (*shadow.GetShadowResp, error) {
	if in.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id 不能为空")
	}

	s, err := l.svcCtx.ShadowModel.FindByDeviceID(l.ctx, in.GetDeviceId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "设备影子不存在")
		}
		l.Errorf("查询影子失败: %v", err)
		return nil, status.Error(codes.Internal, "查询影子失败")
	}

	resp := &shadow.GetShadowResp{
		DeviceId: s.DeviceID,
		Version:  uint32(s.Version),
	}
	if len(s.Desired) > 0 {
		resp.Desired = []byte(s.Desired)
	}
	if len(s.Reported) > 0 {
		resp.Reported = []byte(s.Reported)
	}
	return resp, nil
}
