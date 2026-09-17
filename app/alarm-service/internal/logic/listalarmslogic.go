package logic

import (
	"context"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/search"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// ListAlarmsLogic 历史告警检索逻辑(#41): 强制租户隔离, 支持时间范围/等级/状态/区域/设备/事件类型筛选 + 等级聚合.
//
// 数据源策略: ES 优先, ES 未配置或本次查询失败时降级 MySQL.
// 降级是"可用性优先"的选择, 但必须留痕(Errorf + 语义清晰的 msg), 否则 ES 长期不可用会被静默掩盖.
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

// ListAlarms 分页返回历史告警与等级分布聚合.
func (l *ListAlarmsLogic) ListAlarms(req *types.ListAlarmsReq) (*types.AlarmListResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	// 参数校验早于依赖就绪检查: 非法入参不应被"存储未就绪"掩盖成 500(KI-2 同一原则).
	level, err := validateLevel(req.Level)
	if err != nil {
		return nil, err
	}
	status, err := validateStatus(req.Status)
	if err != nil {
		return nil, err
	}
	start, end, err := timeRange(req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	page, size := normalizePaging(req.Page, req.PageSize)

	if l.svcCtx.Search != nil {
		res, err := l.svcCtx.Search.Search(l.ctx, search.Query{
			TenantID:  tenantID,
			StartTime: derefTime(start),
			EndTime:   derefTime(end),
			Level:     level,
			Status:    status,
			AreaID:    req.AreaId,
			DeviceID:  req.DeviceId,
			EventType: req.EventType,
			Page:      int(page),
			PageSize:  int(size),
		})
		if err == nil {
			return searchResp(res, page, size), nil
		}
		l.Errorf("alarm history es search failed, fallback to mysql: tenant_id=%d err=%v", tenantID, err)
	}

	if l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置)")
	}
	list, total, counts, err := l.svcCtx.Alarms.SearchHistory(l.ctx, model.AlarmHistoryFilter{
		TenantID:  tenantID,
		StartTime: start,
		EndTime:   end,
		Level:     level,
		Status:    status,
		AreaID:    req.AreaId,
		DeviceID:  req.DeviceId,
		EventType: req.EventType,
		Page:      int(page),
		PageSize:  int(size),
	})
	if err != nil {
		l.Errorf("search alarm history failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询历史告警失败")
	}

	return &types.AlarmListResp{
		Total:    total,
		Page:     page,
		PageSize: size,
		List:     alarmItems(list),
		Aggs:     levelAgg(counts),
	}, nil
}

// validateStatus 状态筛选: 0(未处理)/1(已确认)/2(已解决) 为精确筛选, 负数表示不筛选.
//
// 为什么 0 当作"筛选未处理"而不是"未传"": int8 零值无法区分二者, 而 0 是有效状态;
// 想要全部状态必须显式传负数(与规则列表 #37 的约定一致).
// 其余取值直接判非法而不是静默忽略 —— 静默忽略会返回空列表, 把调用方笔误(如 status=9)
// 伪装成"没有数据"。
func validateStatus(status int8) (*int8, error) {
	switch status {
	case model.AlarmStatusPending, model.AlarmStatusAcked, model.AlarmStatusResolved:
		st := status
		return &st, nil
	}
	if status < 0 {
		return nil, nil
	}
	return nil, errorx.NewError(errorx.ErrAlarmParamInvalid,
		"status 仅支持 0(未处理)/1(已确认)/2(已解决), 不筛选请传负数")
}

// searchResp 将 ES 检索结果转换为接口响应.
func searchResp(res *search.Result, page, size int64) *types.AlarmListResp {
	items := make([]types.AlarmItem, 0, len(res.Docs))
	for _, d := range res.Docs {
		items = append(items, types.AlarmItem{
			Id:        d.AlarmID,
			AlarmNo:   d.AlarmNo,
			DeviceId:  d.DeviceID,
			AreaId:    d.AreaID,
			EventType: d.EventType,
			Level:     d.Level,
			Status:    d.Status,
			Content:   d.Content,
			CreatedAt: d.CreateTime.Unix(),
		})
	}

	counts := make([]model.LevelCount, 0, len(res.LevelCounts))
	for _, c := range res.LevelCounts {
		counts = append(counts, model.LevelCount{Level: c.Level, Total: c.Total})
	}

	return &types.AlarmListResp{
		Total:    res.Total,
		Page:     page,
		PageSize: size,
		List:     items,
		Aggs:     levelAgg(counts),
	}
}

// derefTime 取时间指针的值, nil 返回零值(ES 侧以零值表示"不限").
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
