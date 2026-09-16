package logic

import (
	"context"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListAlarmsLogic 告警列表查询逻辑(强制租户隔离, 支持等级/状态/区域/设备筛选).
type ListAlarmsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListAlarmsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAlarmsLogic {
	return &ListAlarmsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListAlarms 分页返回告警列表项.
// status 不传(<0)表示全部状态; 传 0 即活跃告警列表(docs/m3/04 #38).
func (l *ListAlarmsLogic) ListAlarms(req *types.ListAlarmsReq) (*types.AlarmListResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置)")
	}

	f := model.AlarmListFilter{
		TenantID: tenantID,
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	if req.Level > 0 {
		// 等级只有 1-4 四档, 越界直接判参数非法, 避免返回空列表掩盖调用方笔误.
		if req.Level < model.AlarmLevelInfo || req.Level > model.AlarmLevelCritical {
			return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "level 取值范围为 1(提示)~4(紧急)")
		}
		lv := req.Level
		f.Level = &lv
	}
	if req.Status >= 0 {
		st := req.Status
		f.Status = &st
	}
	if req.AreaId > 0 {
		f.AreaID = req.AreaId
	}
	if req.DeviceId != "" {
		f.DeviceID = req.DeviceId
	}

	list, total, err := l.svcCtx.Alarms.List(l.ctx, f)
	if err != nil {
		l.Errorf("list alarms failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询告警列表失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	items := make([]types.AlarmItem, 0, len(list))
	for _, a := range list {
		items = append(items, types.AlarmItem{
			Id:        a.ID,
			AlarmNo:   a.AlarmNo,
			DeviceId:  a.DeviceID,
			AreaId:    a.AreaID,
			EventType: a.EventType,
			Level:     a.Level,
			Status:    a.Status,
			Content:   a.Content,
			CreatedAt: a.CreatedAt.Unix(),
		})
	}

	return &types.AlarmListResp{Total: total, Page: page, PageSize: size, List: items}, nil
}
