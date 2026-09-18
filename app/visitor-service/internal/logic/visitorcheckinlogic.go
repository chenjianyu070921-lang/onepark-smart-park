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

	// 签名校验(防伪造): 验签不通过直接拒绝且不查库.
	// 对外统一报"二维码无效", 不透出具体校验失败原因, 避免帮助攻击者定位绕过路径.
	if e := verifyQRSign(req.QRCode); e != nil {
		l.Errorf("verify qr sign failed: %v", e)
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码无效")
	}

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

	// 1) 核销并置为已签入(RBAC 隔离: 带 tenant_id 条件).
	//
	// 更新条件必须带上 status=待使用(CAS): 上面的"先查后改"在并发重复扫码下会同时读到
	// status=1 并各自写入一次, 结果是重复触发开门 + 签入时间被覆盖。
	// RowsAffected=0 即说明已被另一路请求核销, 此时不能再走开门。
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=? AND status=?", rec.ID, tenantID, model.VisitorStatusPending).
		Updates(map[string]interface{}{
			"status":     model.VisitorStatusCheckin,
			"checkin_at": now,
			"updated_at": now,
		})
	if e := res.Error; e != nil {
		l.Errorf("visitor checkin failed: %v", e)
		return nil, errorx.NewError(errorx.ErrVisitorCheckinFailed, "签入失败")
	}
	if res.RowsAffected == 0 {
		// 并发下第二次扫码走到这里: 返回"已核销"而不是成功, 绝不重复开门.
		l.Infof("visitor checkin skipped, already consumed rec_id=%d", rec.ID)
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}

	// 2) 调 M1 开门并把开门设备ID回填 visitor_record.device_id(失败降级, 不阻断签入).
	deviceID, openMsg := l.openDoor(rec.ID, tenantID)

	// 发布访客事件(评审 P1): checkin → Kafka visitor-event, 供大屏等消费方实时感知; 尽力而为不阻断.
	rec.Status = model.VisitorStatusCheckin
	rec.DeviceID = deviceID
	publishVisitorEvent(l.ctx, l.svcCtx, l.Logger, VisitorEvent{
		Event:        "checkin",
		TenantId:     rec.TenantID,
		VisitorId:    rec.ID,
		VisitorName:  rec.VisitorName,
		VisitorPhone: rec.VisitorPhone,
		InviterId:    rec.InviterID,
		Status:       rec.Status,
		DeviceId:     deviceID,
	})

	return &types.VisitorCheckinResp{
		Id:        rec.ID,
		Status:    model.VisitorStatusCheckin,
		CheckinAt: now.Unix(),
		DeviceID:  deviceID,
		OpenMsg:   openMsg,
	}, nil
}

// openDoorResult 开门降级提示语(场景3 验收口径): 无论 M1 是否开门成功, 签入均不阻断,
// 降级时明确提示"请联系前台人工开门", 由前台兜底放行.
const (
	openMsgSuccess  = "开门成功"
	openMsgDegrade  = "门禁未响应，签入已记录，请联系前台人工开门"
	openMsgNoConfig = "门禁未配置，签入已记录，请联系前台人工开门"
)

// openDoorMsg 开门结果提示语构造(纯函数, 便于降级策略单测).
// 入参: configured M1 开门链路是否已配置(DeviceRPC+门岗设备); opened 开门是否实际成功.
func openDoorMsg(configured, opened bool) string {
	switch {
	case opened:
		return openMsgSuccess
	case !configured:
		return openMsgNoConfig
	default:
		return openMsgDegrade
	}
}

// openDoor 向 M1 device-service 下发开门指令, 并将实际开门设备ID回填到访客记录.
// 入参: recID 访客记录ID, tenantID 园区ID(回填时 RBAC 隔离).
// 返回: 回填的 device_id + 开门结果提示语; M1 未配置/不可达时 device_id 为空、
// 提示"请联系前台人工开门"(降级不阻断签入, 超时由 m1OpenDoorTimeout 短超时控制).
func (l *VisitorCheckinLogic) openDoor(recID, tenantID int64) (string, string) {
	door := l.svcCtx.Config.Door
	if l.svcCtx.DeviceRPC == nil || door.DeviceID == "" {
		l.Infof("skip M1 open door: device rpc or door device not configured (rec_id=%d)", recID)
		return "", openDoorMsg(false, false)
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
		// M1 不可达/超时: 降级, 不阻断访客签入, 明确提示人工兜底.
		l.Errorf("call M1 SendCommand failed, degrade checkin: %v", e)
		return "", openDoorMsg(true, false)
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
	return deviceID, openDoorMsg(true, true)
}
