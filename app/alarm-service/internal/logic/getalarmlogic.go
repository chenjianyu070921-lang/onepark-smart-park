package logic

import (
	"context"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
)

// GetAlarmLogic 告警详情查询逻辑(强制租户隔离).
type GetAlarmLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetAlarmLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetAlarmLogic {
	return &GetAlarmLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetAlarmLogic) GetAlarm(req *types.IdReq) (*types.AlarmDetailResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		// 参数校验类必须用 M3-W-1001 才会返回 400(KI-2); 原先误用 M6-E-0001 会返回 500,
		// 把"调用方没传 x-tenant-id"错误地表达成服务端故障, 与本服务其它接口也不一致.
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置)")
	}

	a, err := l.svcCtx.Alarms.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrNotFound {
			return nil, errorx.NewError(errorx.ErrAlarmNotFound, "告警不存在")
		}
		l.Errorf("get alarm failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询告警详情失败")
	}

	return &types.AlarmDetailResp{
		Id:        a.ID,
		TenantId:  a.TenantID,
		AlarmNo:   a.AlarmNo,
		RuleId:    a.RuleID,
		DeviceId:  a.DeviceID,
		AreaId:    a.AreaID,
		EventType: a.EventType,
		Level:     a.Level,
		Status:    a.Status,
		Content:   a.Content,
		RequestId: a.RequestID,
		AckBy:     a.AckBy,
		AckAt:     timePtrUnix(a.AckAt),
		ResolveBy: a.ResolveBy,
		ResolveAt: timePtrUnix(a.ResolveAt),
		CreatedAt: a.CreatedAt.Unix(),
		UpdatedAt: a.UpdatedAt.Unix(),
	}, nil
}

// timePtrUnix 将 *time.Time 转为秒级时间戳, nil 返回 0.
func timePtrUnix(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}
