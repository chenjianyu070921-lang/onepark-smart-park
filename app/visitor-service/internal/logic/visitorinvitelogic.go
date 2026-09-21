package logic

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/zeromicro/go-zero/core/logx"
)

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

	// 黑名单实时拦截(P2): 邀请阶段即按手机号/身份证拦截被拉黑人员, 禁止生成通行码.
	if blocked, e := checkBlocked(l.ctx, l.svcCtx, tenantID, req.VisitorPhone, req.IdNo); e != nil {
		l.Errorf("check blocklist failed: %v", e)
	} else if blocked {
		return nil, errorx.NewError(errorx.ErrVisitorBlacklisted, "访客已被拉黑, 禁止邀请")
	}

	now := time.Now()
	expire := unixPtr(req.ExpireTime)

	// 生成带签名+有效期的加密二维码内容(可被 M1 门禁扫码校验).
	qr, e := genQRCode(tenantID, inviterID, req.VisitorName, req.ExpireTime, l.svcCtx.Config.QrSignSalt)
	if e != nil {
		l.Errorf("gen qr code failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "生成二维码失败")
	}

	// 核验方式: 未指定(0)默认二维码(1); 其余方式落库以便签入时按对应维度凭证核验.
	channel := req.VerifyChannel
	if channel == 0 {
		channel = 1
	}
	rec := &model.VisitorRecord{
		InviterID:     inviterID,
		VisitorName:   req.VisitorName,
		VisitorPhone:  req.VisitorPhone,
		VisitTime:     unixPtr(req.VisitTime),
		ExpireTime:    expire,
		QRCode:        qr,
		Status:        model.VisitorStatusPending,
		Blacklisted:   0,
		IdNo:          req.IdNo,
		FaceToken:     req.FaceToken,
		BadgeNo:       req.BadgeNo,
		VerifyChannel: channel,
	}
	rec.TenantID = tenantID
	rec.CreatedAt = now
	rec.UpdatedAt = now

	if e := l.svcCtx.DB.WithContext(l.ctx).Create(rec).Error; e != nil {
		l.Errorf("create visitor record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建访客记录失败")
	}

	// go-qrcode 渲染二维码图片(base64 PNG): 渲染属辅助能力, 失败降级不阻断邀请
	// (qr_code 内容串始终可用, 前端可自行渲染兜底).
	qrImage := ""
	if img, e := genQRCodeImage(qr); e != nil {
		l.Errorf("render qr image failed: %v", e)
	} else {
		qrImage = img
	}

	// 发布访客事件(评审 P1): invite → Kafka visitor-event, 供大屏等消费方实时感知; 尽力而为不阻断.
	publishVisitorEvent(l.ctx, l.svcCtx, l.Logger, VisitorEvent{
		Event:        "invite",
		TenantId:     rec.TenantID,
		VisitorId:    rec.ID,
		VisitorName:  rec.VisitorName,
		VisitorPhone: rec.VisitorPhone,
		InviterId:    rec.InviterID,
		Status:       rec.Status,
	})

	return &types.VisitorInviteResp{
		Id:       rec.ID,
		QRCode:   rec.QRCode,
		QrImage:  qrImage,
		ExpireAt: req.ExpireTime,
	}, nil
}

// genQRCode 生成加密二维码内容: base64(JSON{载荷} + 签名).
// 签名 = md5(tenant|inviter|visitor|expire|salt), 防止伪造.
// genQRCode 生成加密二维码内容: base64(JSON{载荷} + 签名).
// salt 为二维码签名盐(来自配置中心), 由调用方透传, 避免硬编码密钥.
func genQRCode(tenantID, inviterID int64, visitorName string, expire int64, salt string) (string, error) {
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
	sign := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%d|%d|%s|%d|%s", tenantID, inviterID, visitorName, expire, salt)))
	return base64.StdEncoding.EncodeToString(append(b, []byte("|"+sign)...)), nil
}

// genQRCodeImage 用 go-qrcode 将加密二维码内容渲染为 PNG 并返回 base64 编码.
// 前端/小程序可直接解码展示; 与 qr_code 内容串完全一致, 两者均可用于扫码签入.
// 入参: content 加密二维码内容串; 返回: base64 PNG, 失败返回 error(调用方降级不阻断邀请).
func genQRCodeImage(content string) (string, error) {
	png, err := qrcode.Encode(content, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

// verifyQRSign 校验二维码内容签名, 防止伪造二维码(伪造内容即使碰巧入库命中也过不了签名关).
// 内容格式: base64( JSON载荷 + "|" + md5签名 ), 与 genQRCode 生成规则严格对应.
// 签名比对使用 constant-time 比较, 防时序攻击.
// 返回: nil 表示验签通过; 非 nil 携带具体原因(仅记日志, 对外统一报"二维码无效"避免泄露校验细节).
// verifyQRSign 校验二维码内容签名, 防止伪造二维码(伪造内容即使碰巧入库命中也过不了签名关).
// salt 为签名盐(来自配置中心), 必须与 genQRCode 生成时使用的盐一致; 由调用方透传.
func verifyQRSign(content string, salt string) error {
	raw, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return errors.New("二维码不是合法的 base64 内容")
	}
	s := string(raw)
	idx := strings.LastIndex(s, "|")
	if idx < 0 {
		return errors.New("二维码缺少签名段")
	}
	payload, sign := s[:idx], s[idx+1:]

	// 从载荷还原参与签名的字段, 按生成时相同顺序拼接重算摘要.
	var p struct {
		TenantID  int64  `json:"tenant_id"`
		InviterID int64  `json:"inviter_id"`
		Visitor   string `json:"visitor"`
		Expire    int64  `json:"expire"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return errors.New("二维码载荷不是合法 JSON")
	}
	expect := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%d|%d|%s|%d|%s", p.TenantID, p.InviterID, p.Visitor, p.Expire, salt)))
	if subtle.ConstantTimeCompare([]byte(expect), []byte(sign)) != 1 {
		return errors.New("二维码签名不匹配")
	}
	return nil
}

// unixPtr 将秒级时间戳转为 *time.Time, 0 返回 nil.
func unixPtr(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0)
	return &t
}
