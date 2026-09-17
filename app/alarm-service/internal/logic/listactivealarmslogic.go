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

// ListActiveAlarmsLogic 活跃告警列表逻辑(docs/m3/04 #38): status 固定为 0(未处理).
//
// 数据源固定 MySQL 而非 ES: 活跃告警是"当前待处置"的小集合, 需要强一致(刚 ack 掉的告警
// 必须立刻从列表消失); ES 写入是异步副本, 用它做活跃列表会出现"已处理仍在列"的错觉.
type ListActiveAlarmsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListActiveAlarmsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListActiveAlarmsLogic {
	return &ListActiveAlarmsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ListActiveAlarms 分页返回未处理告警, 按 level DESC, created_at DESC 排序(严重程度优先).
func (l *ListActiveAlarmsLogic) ListActiveAlarms(req *types.ListActiveAlarmsReq) (*types.AlarmListResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	level, err := validateLevel(req.Level)
	if err != nil {
		return nil, err
	}
	if l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置)")
	}
	page, size := normalizePaging(req.Page, req.PageSize)

	// status 不接受入参, 恒为"未处理": 该接口的语义就是活跃告警, 不允许被调用方改写状态口径.
	pending := model.AlarmStatusPending
	list, total, err := l.svcCtx.Alarms.List(l.ctx, model.AlarmListFilter{
		TenantID: tenantID,
		Status:   &pending,
		Level:    level,
		AreaID:   req.AreaId,
		DeviceID: req.DeviceId,
		Page:     int(page),
		PageSize: int(size),
	})
	if err != nil {
		l.Errorf("list active alarms failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询活跃告警失败")
	}

	return &types.AlarmListResp{
		Total:    total,
		Page:     page,
		PageSize: size,
		List:     alarmItems(list),
	}, nil
}
