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

// VisitorCheckoutLogic 访客签出逻辑: 按ID或二维码置为已签出并记录签出时间.
type VisitorCheckoutLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVisitorCheckoutLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VisitorCheckoutLogic {
	return &VisitorCheckoutLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// VisitorCheckout 处理签出请求.
// 入参: req.Id 或 req.QRCode(二选一).
// 返回: 记录ID/状态/签出时间.
func (l *VisitorCheckoutLogic) VisitorCheckout(req *types.VisitorCheckoutReq) (resp *types.VisitorCheckoutResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 防御: 未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic(审查问题12).
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	q := l.svcCtx.DB.WithContext(l.ctx).Where("tenant_id=?", tenantID)
	switch {
	case req.Id != 0:
		q = q.Where("id=?", req.Id)
	case req.QRCode != "":
		q = q.Where("qr_code=?", req.QRCode)
	default:
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少 id 或 qr_code")
	}

	var rec model.VisitorRecord
	if e := q.First(&rec).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "访客记录不存在")
		}
		l.Errorf("load visitor record failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "加载访客记录失败")
	}

	now := time.Now()
	// CAS: 只允许从"待使用/已签入"流转到已签出, 已签出的记录重复签出时 RowsAffected=0.
	// 不加状态条件时, 重复签出会把 checkout_at 反复覆盖, 通行时长统计随之失真.
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=? AND status IN ?", rec.ID, tenantID,
			[]int8{model.VisitorStatusPending, model.VisitorStatusCheckin}).
		Updates(map[string]interface{}{
			"status":      model.VisitorStatusCheckout,
			"checkout_at": now,
			"updated_at":  now,
		})
	if e := res.Error; e != nil {
		l.Errorf("visitor checkout failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "签出失败")
	}
	if res.RowsAffected == 0 {
		l.Infof("visitor checkout skipped, already checked out rec_id=%d", rec.ID)
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "访客已签出, 无需重复签出")
	}

	// 发布访客事件(评审 P1): checkout → Kafka visitor-event, 供大屏等消费方实时感知; 尽力而为不阻断.
	publishVisitorEvent(l.ctx, l.svcCtx, l.Logger, VisitorEvent{
		Event:        "checkout",
		TenantId:     rec.TenantID,
		VisitorId:    rec.ID,
		VisitorName:  rec.VisitorName,
		VisitorPhone: rec.VisitorPhone,
		InviterId:    rec.InviterID,
		Status:       model.VisitorStatusCheckout,
	})

	return &types.VisitorCheckoutResp{Id: rec.ID, Status: model.VisitorStatusCheckout, CheckoutAt: now.Unix()}, nil
}
