package logic

import (
	"context"

	"onepark/app/notice-service/internal/model"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListNoticesLogic 公告分页查询逻辑(支持类型/状态筛选, 强制租户隔离).
// 列表优先返回置顶项, 其次按创建时间倒序.
type ListNoticesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListNoticesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListNoticesLogic {
	return &ListNoticesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListNotices 分页返回公告列表项.
func (l *ListNoticesLogic) ListNotices(req *types.ListNoticeReq) (resp *types.NoticeListResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误(M2-E-5001)避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.Notice{}).Where("tenant_id=?", tenantID)
	if req.Type != 0 {
		q = q.Where("type=?", req.Type)
	}
	if req.Status != 0 {
		q = q.Where("status=?", req.Status)
	}

	var total int64
	if e := q.Count(&total).Error; e != nil {
		l.Errorf("count notices failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "统计公告失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	var list []model.Notice
	if e := q.Order("top DESC, id DESC").Offset(int((page - 1) * size)).Limit(int(size)).Find(&list).Error; e != nil {
		l.Errorf("list notices failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询公告失败")
	}

	items := make([]types.NoticeItem, 0, len(list))
	for _, n := range list {
		items = append(items, types.NoticeItem{
			Id:        n.ID,
			Title:     n.Title,
			Type:      n.Type,
			Status:    n.Status,
			Top:       n.Top == 1,
			PublishAt: timeOrUnixNotice(n.PublishAt),
			CreatedAt: n.CreatedAt.Unix(),
		})
	}

	return &types.NoticeListResp{Total: total, List: items}, nil
}
