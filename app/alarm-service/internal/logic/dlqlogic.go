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

// ListDeadLettersLogic 死信台账分页列表(docs/m3/06 §5.4): 让"进了台账的消息"可被运营看见.
type ListDeadLettersLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListDeadLettersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListDeadLettersLogic {
	return &ListDeadLettersLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *ListDeadLettersLogic) ListDeadLetters(req *types.ListDLQReq) (*types.ListDLQResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if l.svcCtx.DeadLetters == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "死信台账未就绪(MySQL 未配置)")
	}

	f := model.DeadLetterListFilter{
		TenantID: tenantID,
		DeviceID: req.DeviceId,
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	// status 不传时 int8 零值与"待处理"同义, 约定: 负数表示不筛选(与告警列表口径一致).
	if req.Status == model.DLQStatusPending || req.Status == model.DLQStatusReplayed || req.Status == model.DLQStatusDropped {
		status := req.Status
		f.Status = &status
	} else if req.Status >= 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "status 仅支持 0(待处理)/1(已重放)/2(已丢弃), 查全部请传负数")
	}

	list, total, err := l.svcCtx.DeadLetters.List(l.ctx, f)
	if err != nil {
		l.Errorf("list dead letters failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询死信台账失败")
	}

	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}

	items := make([]types.DLQItem, 0, len(list))
	for _, d := range list {
		items = append(items, toDLQItem(d))
	}
	return &types.ListDLQResp{Total: total, Page: page, PageSize: size, List: items}, nil
}

func toDLQItem(d *model.AlarmDLQ) types.DLQItem {
	return types.DLQItem{
		Id:          d.ID,
		Topic:       d.Topic,
		PartitionNo: d.PartitionNo,
		MsgOffset:   d.MsgOffset,
		RequestId:   d.RequestID,
		DeviceId:    d.DeviceID,
		EventType:   d.EventType,
		Payload:     d.Payload,
		ErrorMsg:    d.ErrorMsg,
		RetryCount:  d.RetryCount,
		Status:      d.Status,
		CreatedAt:   d.CreatedAt.Unix(),
	}
}

// ReplayDeadLetterLogic 重放一条死信: 用原始报文重新走消费主链路, 成功才置为已重放.
//
// 关键取舍: 重放失败**不更新台账状态**(保持待处理), 让运维可以修复依赖后再次重放;
// 失败原因随错误返回, 不吞异常 —— 死信的价值就在于"失败可见", 标记为已重放会让它彻底消失.
type ReplayDeadLetterLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewReplayDeadLetterLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ReplayDeadLetterLogic {
	return &ReplayDeadLetterLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

func (l *ReplayDeadLetterLogic) ReplayDeadLetter(req *types.IdReq) (*types.ReplayDLQResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrAlarmParamInvalid, "死信ID非法")
	}
	// 重放依赖完整消费链路(存储/去重/规则引擎), 任一未就绪都不应给出"已重放"的假象.
	if l.svcCtx.DeadLetters == nil || l.svcCtx.Alarms == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "告警存储未就绪(MySQL 未配置), 无法重放")
	}

	entry, err := l.svcCtx.DeadLetters.FindByID(l.ctx, tenantID, req.Id)
	if err != nil {
		if err == model.ErrDLQNotFound {
			return nil, errorx.NewError(errorx.ErrAlarmDLQNotFound, "死信记录不存在")
		}
		l.Errorf("get dead letter failed: %v", err)
		return nil, errorx.NewError(errorx.ErrAlarmQuery, "查询死信记录失败")
	}

	if err := l.svcCtx.ReplayDeadLetter(l.ctx, entry); err != nil {
		l.Errorf("replay dead letter failed id=%d request_id=%s err=%v", entry.ID, entry.RequestID, err)
		return nil, errorx.NewError(errorx.ErrAlarmDLQReplay, "重放失败: "+err.Error())
	}

	if err := l.svcCtx.DeadLetters.MarkReplayed(l.ctx, tenantID, req.Id); err != nil {
		// 主链路已成功但状态没改成: 必须报出来, 否则该死信会被反复重放.
		l.Errorf("mark dead letter replayed failed id=%d err=%v", req.Id, err)
		return nil, errorx.NewError(errorx.ErrAlarmDLQReplay, "重放已执行但更新台账状态失败: "+err.Error())
	}
	return &types.ReplayDLQResp{Id: req.Id, Replayed: true}, nil
}
