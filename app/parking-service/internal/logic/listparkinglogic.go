package logic

import (
	"context"
	"fmt"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListParkingLogic 停车记录分页查询逻辑(支持状态/车牌筛选, 强制租户隔离).
type ListParkingLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListParkingLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListParkingLogic {
	return &ListParkingLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListParking 分页返回停车记录列表项.
func (l *ListParkingLogic) ListParking(req *types.ListParkingReq) (resp *types.ParkingListResp, err error) {
	return l.query(req.Page, req.PageSize, req.Status, req.PlateNo)
}

// query 通用分页查询(强制 tenant_id, 可选 status/plate 过滤).
func (l *ListParkingLogic) query(page, pageSize int64, status int8, plate string) (*types.ParkingListResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.ParkingRecord{}).Where("tenant_id=?", tenantID)
	if status != 0 {
		q = q.Where("status=?", status)
	}
	if plate != "" {
		q = q.Where("plate_no=?", plate)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count parking records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计停车失败")
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	var list []model.ParkingRecord
	if e := q.Order("id DESC").Offset(int((page - 1) * pageSize)).Limit(int(pageSize)).Find(&list).Error; e != nil {
		l.Errorf("list parking records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询停车失败")
	}

	items := make([]types.ParkingItem, 0, len(list))
	for _, p := range list {
		items = append(items, types.ParkingItem{
			Id:        p.ID,
			PlateNo:   p.PlateNo,
			EntryTime: timeOrUnix(p.EntryTime),
			ExitTime:  timeOrUnix(p.ExitTime),
			Status:    p.Status,
			Fee:       fmt.Sprintf("%.2f", p.Fee),
		})
	}

	return &types.ParkingListResp{Total: total, List: items}, nil
}
