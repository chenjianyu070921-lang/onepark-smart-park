package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListMonthlyCardsLogic 月卡分页列表逻辑(P2, 含到期提醒筛选).
type ListMonthlyCardsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListMonthlyCardsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListMonthlyCardsLogic {
	return &ListMonthlyCardsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListMonthlyCards 分页返回月卡列表.
// 筛选: status(1生效/2停用, 0不限) / plate_no 模糊 / expiring_days(N 天内到期, 到期提醒口径,
// 自动限定 status=生效且 end_time 落在 [now, now+N天]); 排序按到期时间升序(最快到期的排最前).
func (l *ListMonthlyCardsLogic) ListMonthlyCards(req *types.ListMonthlyCardsReq) (resp *types.MonthlyCardListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.MonthlyCard{}).Where("tenant_id=?", tenantID)

	if req.Status != 0 {
		q = q.Where("status=?", req.Status)
	}
	if p := strings.TrimSpace(req.PlateNo); p != "" {
		q = q.Where("plate_no LIKE ?", "%"+p+"%")
	}

	// 到期提醒: N 天内到期的生效月卡(含已过期但未停用的, 提醒语义应覆盖"刚过期").
	now := time.Now()
	if req.ExpiringDays > 0 {
		q = q.Where("status=? AND end_time>=? AND end_time<=?",
			model.MonthlyCardStatusActive, now, now.Add(time.Duration(req.ExpiringDays)*24*time.Hour))
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count monthly cards failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计月卡失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.MonthlyCard
	if e := q.Order("end_time ASC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list monthly cards failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询月卡失败")
	}

	items := make([]types.MonthlyCardItem, 0, len(list))
	for _, c := range list {
		daysLeft := int64(c.EndTime.Sub(now).Hours() / 24)
		if daysLeft < 0 {
			daysLeft = 0 // 已过期未停用: 剩余天数归零, 提醒处理
		}
		items = append(items, types.MonthlyCardItem{
			Id:        c.ID,
			PlateNo:   c.PlateNo,
			OwnerName: c.OwnerName,
			Phone:     c.Phone,
			StartTime: c.StartTime.Unix(),
			EndTime:   c.EndTime.Unix(),
			Status:    c.Status,
			DaysLeft:  daysLeft,
		})
	}

	return &types.MonthlyCardListResp{Total: total, List: items}, nil
}
