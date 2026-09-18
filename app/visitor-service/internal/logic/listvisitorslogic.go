package logic

import (
	"context"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/rbac"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListVisitorsLogic 访客记录分页查询逻辑(强制租户隔离).
type ListVisitorsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListVisitorsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListVisitorsLogic {
	return &ListVisitorsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListVisitors 分页返回访客列表项.
func (l *ListVisitorsLogic) ListVisitors(req *types.ListVisitorReq) (resp *types.VisitorListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误(M2-E-5001)避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).Where("tenant_id=?", tenantID)

	// RBAC 行级隔离: 业主仅能查看自己发起的邀请(inviter_id), 其余受限角色拒绝查看.
	uid := ctxdata.GetUserId(l.ctx)
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if !rbac.IsFullScope(roles) {
		if rbac.HasRole(roles, rbac.RoleOwner) {
			q = q.Where("inviter_id=?", uid)
		} else {
			q = q.Where("1=0")
		}
	}

	if req.Status != 0 {
		q = q.Where("status=?", req.Status)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count visitor records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计访客失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.VisitorRecord
	if e := q.Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list visitor records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询访客失败")
	}

	items := make([]types.VisitorItem, 0, len(list))
	for _, v := range list {
		items = append(items, types.VisitorItem{
			Id:           v.ID,
			VisitorName:  v.VisitorName,
			VisitorPhone: v.VisitorPhone,
			Status:       v.Status,
			VisitTime:    timeOrZero(v.VisitTime),
			CheckinAt:    timeOrZero(v.CheckinAt),
			CheckoutAt:   timeOrZero(v.CheckoutAt),
			DeviceID:     v.DeviceID,
		})
	}

	return &types.VisitorListResp{Total: total, List: items}, nil
}

// timeOrZero 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timeOrZero(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
