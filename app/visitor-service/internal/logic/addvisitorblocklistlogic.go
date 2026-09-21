package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// AddVisitorBlocklistLogic 新增/更新访客黑名单逻辑.
// 去重: 同租户下"已生效"且同手机号或同身份证的, 刷新其字段与生效状态; 否则新增一行.
type AddVisitorBlocklistLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAddVisitorBlocklistLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AddVisitorBlocklistLogic {
	return &AddVisitorBlocklistLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// AddVisitorBlocklist 新增或刷新访客黑名单.
// 入参: 姓名/手机号/身份证(手机号或身份证至少一)/原因/生效时间窗(可选).
// 返回: 黑名单记录ID.
func (l *AddVisitorBlocklistLogic) AddVisitorBlocklist(req *types.AddVisitorBlocklistReq) (resp *types.AddVisitorBlocklistResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	operatorID := ctxdata.GetUserId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	if req.Phone == "" && req.IdNo == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "手机号或身份证至少提供一个拉黑维度")
	}

	now := time.Now()
	bl := &model.VisitorBlocklist{
		VisitorName:   req.VisitorName,
		Phone:         req.Phone,
		IdNo:          req.IdNo,
		Reason:        req.Reason,
		EffectiveFrom: unixPtrBL(req.EffectiveFrom),
		EffectiveTo:   unixPtrBL(req.EffectiveTo),
		Status:        model.BlocklistStatusActive,
		OperatorID:    operatorID,
	}
	bl.TenantID = tenantID
	bl.CreatedAt = now
	bl.UpdatedAt = now

	// 查同租户下已生效且(手机号或身份证)命中之一的记录, 命中则刷新, 否则新增.
	var exist model.VisitorBlocklist
	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorBlocklist{}).
		Where("tenant_id=? AND status=?", tenantID, model.BlocklistStatusActive)
	conds := []string{}
	args := []interface{}{}
	if req.Phone != "" {
		conds = append(conds, "phone = ?")
		args = append(args, req.Phone)
	}
	if req.IdNo != "" {
		conds = append(conds, "id_no = ?")
		args = append(args, req.IdNo)
	}
	q = q.Where(strings.Join(conds, " OR "), args...)
	if e := q.First(&exist).Error; e == nil {
		if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorBlocklist{}).
			Where("id=? AND tenant_id=?", exist.ID, tenantID).
			Updates(map[string]interface{}{
				"visitor_name":   req.VisitorName,
				"phone":          req.Phone,
				"id_no":          req.IdNo,
				"reason":         req.Reason,
				"effective_from": bl.EffectiveFrom,
				"effective_to":   bl.EffectiveTo,
				"status":         model.BlocklistStatusActive,
				"operator_id":    operatorID,
				"updated_at":     now,
			}).Error; e != nil {
			return nil, errorx.NewError(errorx.ErrM2Internal, "更新黑名单失败")
		}
		return &types.AddVisitorBlocklistResp{Id: exist.ID}, nil
	} else if e != gorm.ErrRecordNotFound {
		l.Errorf("query visitor blocklist failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询黑名单失败")
	}

	if e := l.svcCtx.DB.WithContext(l.ctx).Create(bl).Error; e != nil {
		l.Errorf("create visitor blocklist failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "新增黑名单失败")
	}
	return &types.AddVisitorBlocklistResp{Id: bl.ID}, nil
}

// unixPtrBL 秒级时间戳转 *time.Time, 0 返回 nil(用于黑名单生效时间窗).
func unixPtrBL(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0)
	return &t
}
