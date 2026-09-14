package logic

import (
	"context"
	"encoding/json"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
)

// ParkingEntryLogic 车辆入场逻辑(HTTP 入口, 便于联调; 生产由 Kafka 地磁遥测驱动).
type ParkingEntryLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewParkingEntryLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ParkingEntryLogic {
	return &ParkingEntryLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ParkingEntry 创建停车中记录, 并发布车辆入场事件.
func (l *ParkingEntryLogic) ParkingEntry(req *types.ParkingEntryReq) (resp *types.ParkingRecordResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	now := time.Now()
	rec := &model.ParkingRecord{
		PlateNo:     req.PlateNo,
		VehicleType: req.VehicleType,
		DeviceIDIn:  req.DeviceIDIn,
		EntryTime:   &now,
		Status:      model.ParkingStatusParking,
	}
	rec.TenantID = tenantID
	rec.CreatedAt = now
	rec.UpdatedAt = now

	if e := l.svcCtx.DB.WithContext(l.ctx).Create(rec).Error; e != nil {
		l.Errorf("create parking record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建停车记录失败")
	}

	// 发布入场事件(与 Kafka 遥测路径一致).
	if l.svcCtx.Producer != nil {
		b, _ := json.Marshal(rec)
		if e := l.svcCtx.Producer.Publish(l.ctx, kafka.TopicParkingEntry, []byte(req.PlateNo), b); e != nil {
			l.Errorf("publish parking-entry failed: %v", e)
		}
	}

	return &types.ParkingRecordResp{
		Id:        rec.ID,
		PlateNo:   rec.PlateNo,
		EntryTime: now.Unix(),
		Status:    rec.Status,
	}, nil
}
