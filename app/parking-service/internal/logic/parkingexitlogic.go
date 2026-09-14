package logic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/kafka"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// ParkingExitLogic 车辆离场逻辑: 更新停车中记录, 计算时长与费用, 置已完成.
type ParkingExitLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewParkingExitLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ParkingExitLogic {
	return &ParkingExitLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ParkingExit 处理离场请求, 触发计费.
// 入参: req.PlateNo 车牌, req.DeviceIDOut 出场设备.
// 返回: 停车记录响应(含费用字符串).
func (l *ParkingExitLogic) ParkingExit(req *types.ParkingExitReq) (resp *types.ParkingRecordResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	var rec model.ParkingRecord
	if e := l.svcCtx.DB.WithContext(l.ctx).
		Where("plate_no=? AND tenant_id=? AND status=?", req.PlateNo, tenantID, model.ParkingStatusParking).
		Order("id DESC").First(&rec).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrParkingRecordNotFound, "未找到在场停车记录")
		}
		l.Errorf("load parking record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载停车记录失败")
	}

	now := time.Now()
	dur := 0
	if rec.EntryTime != nil {
		dur = int(now.Sub(*rec.EntryTime).Minutes())
	}
	fee := svc.CalcFee(rec.EntryTime, &now, rec.VehicleType)

	updates := map[string]interface{}{
		"status":        model.ParkingStatusDone,
		"exit_time":     now,
		"duration_min":  dur,
		"fee":           fee,
		"device_id_out": req.DeviceIDOut,
		"updated_at":    now,
	}
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&rec).Updates(updates).Error; e != nil {
		l.Errorf("update parking record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "离场计费失败")
	}

	// 发布离场事件 + 异常车辆告警.
	if l.svcCtx.Producer != nil {
		b, _ := json.Marshal(rec)
		_ = l.svcCtx.Producer.Publish(l.ctx, kafka.TopicParkingExit, []byte(req.PlateNo), b)
		if rec.VehicleType == model.VehicleTypeAbnormal {
			alarm, _ := json.Marshal(map[string]interface{}{
				"device_id": req.DeviceIDOut,
				"severity":  2,
				"content":   fmt.Sprintf("异常车辆 %s 离场", req.PlateNo),
				"timestamp": now.Unix(),
			})
			_ = l.svcCtx.Producer.Publish(l.ctx, kafka.TopicAlarm, []byte(req.PlateNo), alarm)
		}
	}

	return &types.ParkingRecordResp{
		Id:          rec.ID,
		PlateNo:     rec.PlateNo,
		EntryTime:   timeOrUnix(rec.EntryTime),
		ExitTime:    now.Unix(),
		DurationMin: int64(dur),
		Fee:         fmt.Sprintf("%.2f", fee),
		Status:      model.ParkingStatusDone,
	}, nil
}

// timeOrUnix 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timeOrUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
