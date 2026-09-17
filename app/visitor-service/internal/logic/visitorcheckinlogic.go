package logic

import (
	"context"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// m1OpenDoorTimeout M1 开门 gRPC 短超时(设计文档要求: 访客开门设短超时, 失败明确提示).
const m1OpenDoorTimeout = 800 * time.Millisecond

// VisitorCheckinLogic 访客扫码签入逻辑: 核销二维码 + 触发 M1 开门(gRPC SendCommand).
type VisitorCheckinLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVisitorCheckinLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VisitorCheckinLogic {
	return &VisitorCheckinLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// VisitorCheckin 处理扫码签入: 校验二维码状态与有效期, 置为已签入, 并调用 M1 开门.
// 入参: req.QRCode 加密二维码内容.
// 返回: 记录ID/状态/签入时间/开门设备ID.
// 降级: M1 不可达或未配置时跳过开门, 签入仍然成功(device_id 保持为空).
func (l *VisitorCheckinLogic) VisitorCheckin(req *types.VisitorCheckinReq) (resp *types.VisitorCheckinResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	var rec model.VisitorRecord
	if e := l.svcCtx.DB.WithContext(l.ctx).Where("qr_code=? AND tenant_id=?", req.QRCode, tenantID).First(&rec).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码无效")
		}
		l.Errorf("load visitor record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载访客记录失败")
	}

	now := time.Now()
	// 过期校验: 超过过期时间置为已过期并拒绝签入.
	if rec.ExpireTime != nil && now.After(*rec.ExpireTime) {
		if rec.Status != model.VisitorStatusExpired {
			_ = l.svcCtx.DB.WithContext(l.ctx).Model(&rec).Updates(map[string]interface{}{"status": model.VisitorStatusExpired, "updated_at": now}).Error
		}
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeExpired, "二维码已过期")
	}
	// 已签入/已签出不允许重复核销.
	if rec.Status == model.VisitorStatusCheckin || rec.Status == model.VisitorStatusCheckout {
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}

	// 1) 核销并置为已签入.
	// 并发安全: 必须带 status=待使用 原子条件, 防止同一二维码被并发签入两次导致重复开门.
	// 利用 MySQL 行级 UPDATE 的原子性: 仅当记录仍为待使用时才更新成功(RowsAffected==1),
	// 并发的第二次请求命中 status 已变更 → RowsAffected==0 → 判定为已被核销.
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=? AND status=?", rec.ID, tenantID, model.VisitorStatusPending).
		Updates(map[string]interface{}{
			"status":     model.VisitorStatusCheckin,
			"checkin_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		l.Errorf("visitor checkin failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrVisitorCheckinFailed, "签入失败")
	}
	if res.RowsAffected == 0 {
		// 同一条码已被并发/重复核销(状态已非待使用), 直接拒绝, 避免重复触发 M1 开门.
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}

	// 2) 调 M1 开门并把开门设备ID回填 visitor_record.device_id(失败降级, 不阻断签入).
	deviceID := l.openDoor(rec.ID, tenantID)

	return &types.VisitorCheckinResp{
		Id:        rec.ID,
		Status:    model.VisitorStatusCheckin,
		CheckinAt: now.Unix(),
		DeviceID:  deviceID,
	}, nil
}

// openDoor 向 M1 device-service 下发开门指令, 并将实际开门设备ID回填到访客记录.
// 入参: recID 访客记录ID, tenantID 园区ID(回填时 RBAC 隔离).
// 返回: 回填的 device_id; M1 未配置/不可达时返回空串并仅记日志(降级不阻断签入).
func (l *VisitorCheckinLogic) openDoor(recID, tenantID int64) string {
	door := l.svcCtx.Config.Door
	if l.svcCtx.DeviceRPC == nil || door.DeviceID == "" {
		l.Infof("skip M1 open door: device rpc or door device not configured (rec_id=%d)", recID)
		return ""
	}

	command := door.Command
	if command == "" {
		command = "open_door"
	}

	ctx, cancel := context.WithTimeout(l.ctx, m1OpenDoorTimeout)
	defer cancel()
	res, e := l.svcCtx.DeviceRPC.SendCommand(ctx, &commonpb.DeviceCommand{
		DeviceId: door.DeviceID,
		Command:  command,
	})
	if e != nil {
		// M1 不可达: 降级, 不阻断访客签入.
		l.Errorf("call M1 SendCommand failed, degrade checkin: %v", e)
		return ""
	}

	// 优先使用 M1 返回的实际开门设备ID, 缺省回落到配置的门岗设备ID.
	deviceID := door.DeviceID
	if res != nil && res.DeviceId != "" {
		deviceID = res.DeviceId
		l.Infof("M1 open door success: rec_id=%d device_id=%s", recID, deviceID)
	}

	// 回填 device_id.
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=?", recID, tenantID).
		Update("device_id", deviceID).Error; e != nil {
		l.Errorf("backfill visitor device_id failed: %v", e)
	}
	return deviceID
}
