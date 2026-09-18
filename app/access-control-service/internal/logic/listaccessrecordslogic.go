package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ListAccessRecordsLogic 通行记录查询逻辑(docs/m3/04 #48): 分页 + 人员/设备/结果/方式/时间范围筛选.
type ListAccessRecordsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListAccessRecordsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAccessRecordsLogic {
	return &ListAccessRecordsLogic{ctx: ctx, svcCtx: svcCtx}
}

// ListAccessRecords 返回分页通行记录(强制租户隔离, 按 created_at DESC 排序).
func (l *ListAccessRecordsLogic) ListAccessRecords(req *types.ListAccessRecordsReq) (*types.ListAccessRecordsResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Records == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "通行记录存储未就绪(MySQL 未配置)")
	}

	f := model.AccessRecordFilter{
		TenantID: tenantID,
		PersonID: req.PersonId,
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	if req.DeviceId != "" {
		f.DeviceID = strings.TrimSpace(req.DeviceId)
	}
	// Result 不传时 int8 零值是 0(失败), 与"查失败记录"同义不可区分 —— 规定: <0 表示不筛选.
	if req.Result >= 0 {
		if req.Result != model.AccessResultFail && req.Result != model.AccessResultSuccess {
			return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "result 仅支持 0(失败)/1(成功)")
		}
		result := req.Result
		f.Result = &result
	}
	if req.OpenType != "" {
		if !isKnownOpenType(req.OpenType) {
			return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "open_type 仅支持 card/face/remote/qrcode")
		}
		f.OpenType = req.OpenType
	}
	if req.StartTime > 0 {
		start := time.Unix(req.StartTime, 0)
		f.StartTime = &start
	}
	if req.EndTime > 0 {
		end := time.Unix(req.EndTime, 0)
		f.EndTime = &end
	}
	if f.StartTime != nil && f.EndTime != nil && f.EndTime.Before(*f.StartTime) {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "end_time 不能早于 start_time")
	}

	list, total, err := l.svcCtx.Records.List(l.ctx, f)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessRecord, "查询通行记录失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	items := make([]types.AccessRecordItem, 0, len(list))
	for _, r := range list {
		items = append(items, types.AccessRecordItem{
			Id:         r.ID,
			PersonId:   r.PersonID,
			DeviceId:   r.DeviceID,
			Result:     r.Result,
			OpenType:   r.OpenType,
			FailReason: r.FailReason,
			CreatedAt:  r.CreatedAt.Unix(),
		})
	}
	return &types.ListAccessRecordsResp{Total: total, Page: page, PageSize: size, List: items}, nil
}

// isKnownOpenType 判定开门方式取值是否合法(§2.2 定义四种).
func isKnownOpenType(t string) bool {
	switch t {
	case model.OpenTypeCard, model.OpenTypeFace, model.OpenTypeRemote, model.OpenTypeQRCode:
		return true
	default:
		return false
	}
}
