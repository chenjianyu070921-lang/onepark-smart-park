package dispatch

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/dispatch-service/internal/model"
	"onepark/app/dispatch-service/internal/state"
	"onepark/app/dispatch-service/internal/svc"
	"onepark/app/dispatch-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/app/dispatch-service/internal/ecode"
	"onepark/common/errorx"
)

// TaskCreateLogic 人工创建调度工单.
// 新建工单初始状态固定为「待指派」.
type TaskCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewTaskCreateLogic 构造创建调度工单逻辑.
func NewTaskCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *TaskCreateLogic {
	return &TaskCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// TaskCreate 创建调度工单并写入初始审计.
func (l *TaskCreateLogic) TaskCreate(req *types.TaskCreateReq) (*types.TaskCreateResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}
	// 租户为 RBAC 行级隔离维度, 必须由网关注入, 不允许匿名建单.
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "缺少租户信息(x-tenant-id)")
	}
	if req.Title == "" {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "工单标题不能为空")
	}
	if req.ZoneCode == "" {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "事发区域不能为空")
	}
	if !validPriority(req.Priority) {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "优先级取值非法, 仅支持 1/2/3")
	}
	// 告警来源的工单只允许由 Kafka 消费者自动落库, 防止人工伪造 source 绕过去重逻辑.
	if int8(req.Source) == model.SourceAlarm {
		return nil, errorx.NewError(ecode.ErrDispatchParamInvalid, "告警来源工单由消费者自动创建, 不支持人工指定")
	}

	task := &model.DispatchTask{
		TenantID:    tenantID,
		TaskNo:      model.NewTaskNo(),
		Title:       req.Title,
		Source:      model.SourceManual,
		ZoneCode:    req.ZoneCode,
		Priority:    int8(req.Priority),
		Status:      model.StatusPendingAssign,
		Description: req.Description,
	}

	if err := l.svcCtx.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&model.DispatchTaskLog{
			TenantID:   tenantID,
			TaskId:     task.Id,
			FromStatus: 0,
			ToStatus:   model.StatusPendingAssign,
			Action:     state.ActionCreate,
			Remark:     "人工创建",
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error
	}); err != nil {
		l.Errorf("[dispatch] create task failed: %v", err)
		return nil, errorx.NewError(ecode.ErrTaskCreateFailed, "创建调度工单失败")
	}

	return &types.TaskCreateResp{
		Id:     task.Id,
		TaskNo: task.TaskNo,
		Status: int32(task.Status),
	}, nil
}
