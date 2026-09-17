package logic

import (
	"context"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListAccessRecordsLogic 门禁通行记录分页查询逻辑(支持按点位过滤, 强制租户隔离).
type ListAccessRecordsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListAccessRecordsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAccessRecordsLogic {
	return &ListAccessRecordsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListAccessRecords 分页返回通行记录列表项(远程开门审计).
func (l *ListAccessRecordsLogic) ListAccessRecords(req *types.ListAccessRecordsReq) (resp *types.AccessRecordListResp, err error) {
	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误(M6-E-0005)避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrInternal, "数据库未初始化")
	}
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 统一在 WHERE 上追加 tenant_id, 保证 RBAC 行级隔离.
	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.AccessRecord{}).Where("tenant_id=?", tenantID)
	if req.GateID != 0 {
		q = q.Where("gate_id=?", req.GateID)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count access records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrAccessRecord, "统计通行记录失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.AccessRecord
	if e := q.Order("id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list access records failed: %v", e)
		return nil, errorx.NewError(errorx.ErrAccessRecord, "查询通行记录失败")
	}

	items := make([]types.AccessRecordItem, 0, len(list))
	for _, r := range list {
		items = append(items, types.AccessRecordItem{
			Id:         r.ID,
			GateID:     r.GateID,
			DeviceID:   r.DeviceID,
			Action:     r.Action,
			Result:     r.Result,
			OperatorID: r.OperatorID,
			Remark:     r.Remark,
			CreatedAt:  r.CreatedAt.Unix(),
		})
	}

	return &types.AccessRecordListResp{Total: total, List: items}, nil
}
