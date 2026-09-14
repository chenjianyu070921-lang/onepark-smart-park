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
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&rec).Updates(map[string]interface{}{
		"status":      model.VisitorStatusCheckout,
		"checkout_at": now,
		"updated_at":  now,
	}).Error; e != nil {
		l.Errorf("visitor checkout failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "签出失败")
	}

	return &types.VisitorCheckoutResp{Id: rec.ID, Status: model.VisitorStatusCheckout, CheckoutAt: now.Unix()}, nil
}
