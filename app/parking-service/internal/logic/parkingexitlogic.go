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
	// 计费: 优先采用当前生效的配置规则; 无配置/解析失败降级为内置默认(CalcFee).
	fee := svc.CalcFee(rec.EntryTime, &now, rec.VehicleType)
	if rule, rerr := model.GetActiveParkingFeeRule(l.svcCtx.DB, tenantID, now); rerr == nil && rule != nil {
		if cfg, perr := rule.ParseRule(); perr == nil {
			fee = svc.CalcFeeByRule(rec.EntryTime, &now, rec.VehicleType, cfg)
		}
	}

	updates := map[string]interface{}{
		"status":        model.ParkingStatusDone,
		"exit_time":     now,
		"duration_min":  dur,
		"fee":           fee,
		"device_id_out": req.DeviceIDOut,
		"updated_at":    now,
	}
	// CAS: 更新必须带 status=停车中 条件(与消费端 onTelemetryExit 一致), 防止并发/重试离场重复计费并重复广播(审查问题3).
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.ParkingRecord{}).
		Where("id=? AND tenant_id=? AND status=?", rec.ID, tenantID, model.ParkingStatusParking).
		Updates(updates)
	if res.Error != nil {
		l.Errorf("update parking record failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrM2Internal, "离场计费失败")
	}
	if res.RowsAffected == 0 {
		// 已被其它离场请求结算(或人工结单): 本次不再重复计费与广播.
		return nil, errorx.NewError(errorx.ErrM2Internal, "该停车记录已结算或不存在")
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
