package logic

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// 二维码签名盐, 实际应来自配置中心/密钥管理, 此处仅作演示.
const qrSignSalt = "onepark-m2-visitor"

// VisitorInviteLogic 发起访客邀请逻辑: 业主/物业邀请, 生成加密二维码.
type VisitorInviteLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVisitorInviteLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VisitorInviteLogic {
	return &VisitorInviteLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// VisitorInvite 生成访客通行二维码记录.
// 入参: 访客姓名/手机号/预期到访时间/过期时间/来访原因(可选).
// 返回: 记录ID/加密二维码内容/过期时间.
func (l *VisitorInviteLogic) VisitorInvite(req *types.VisitorInviteReq) (resp *types.VisitorInviteResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	inviterID := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	now := time.Now()
	expire := unixPtr(req.ExpireTime)

	// 生成带签名+有效期的加密二维码内容(可被 M1 门禁扫码校验).
	qr, e := genQRCode(tenantID, inviterID, req.VisitorName, req.ExpireTime)
	if e != nil {
		l.Errorf("gen qr code failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "生成二维码失败")
	}

	rec := &model.VisitorRecord{
		InviterID:    inviterID,
		VisitorName:  req.VisitorName,
		VisitorPhone: req.VisitorPhone,
		VisitTime:    unixPtr(req.VisitTime),
		ExpireTime:   expire,
		QRCode:       qr,
		Status:       model.VisitorStatusPending,
		Blacklisted:  0,
	}
	rec.TenantID = tenantID
	rec.CreatedAt = now
	rec.UpdatedAt = now

	if e := l.svcCtx.DB.WithContext(l.ctx).Create(rec).Error; e != nil {
		l.Errorf("create visitor record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建访客记录失败")
	}

	return &types.VisitorInviteResp{
		Id:       rec.ID,
		QRCode:   rec.QRCode,
		ExpireAt: req.ExpireTime,
	}, nil
}

// genQRCode 生成加密二维码内容: base64(JSON{载荷} + 签名).
// 签名 = md5(tenant|inviter|visitor|expire|salt), 防止伪造.
func genQRCode(tenantID, inviterID int64, visitorName string, expire int64) (string, error) {
	payload := map[string]interface{}{
		"tenant_id":  tenantID,
		"inviter_id": inviterID,
		"visitor":    visitorName,
		"expire":     expire,
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return "", e
	}
	sign := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%d|%d|%s|%d|%s", tenantID, inviterID, visitorName, expire, qrSignSalt))))
	return base64.StdEncoding.EncodeToString(append(b, []byte("|"+sign)...)), nil
}

// unixPtr 将秒级时间戳转为 *time.Time, 0 返回 nil.
func unixPtr(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0)
	return &t
}
