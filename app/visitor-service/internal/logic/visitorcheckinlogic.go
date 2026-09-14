package logic

import (
	"context"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// VisitorCheckinLogic 访客扫码签入逻辑: 核销二维码 + 触发 M1 开门(M1 另通过 gRPC 下发指令).
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

// VisitorCheckin 处理扫码签入: 校验二维码状态与有效期, 置为已签入.
// 入参: req.QRCode 加密二维码内容.
// 返回: 记录ID/状态/签入时间.
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

	// 更新为已签入; device_id 由 M1 开门成功后回填(此处先留空, 标记待开门).
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&rec).Updates(map[string]interface{}{
		"status":     model.VisitorStatusCheckin,
		"checkin_at": now,
		"updated_at": now,
	}).Error; e != nil {
		l.Errorf("visitor checkin failed: %v", e)
		return nil, errorx.NewError(errorx.ErrVisitorCheckinFailed, "签入失败")
	}

	// TODO(M1联动): 调用 DeviceService.SendCommand 下发开门指令, 并将返回 device_id 回填 rec.DeviceID.
	// 当前 M1 地址未配置时跳过, 由独立通道完成开门.

	return &types.VisitorCheckinResp{Id: rec.ID, Status: model.VisitorStatusCheckin, CheckinAt: now.Unix()}, nil
}
