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
	if req.Title == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "工单标题不能为空")
	}
	if req.ZoneCode == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "事发区域不能为空")
	}
	if !validPriority(req.Priority) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "优先级取值非法, 仅支持 1/2/3")
	}
	// 告警来源的工单只允许由 Kafka 消费者自动落库, 防止人工伪造 source 绕过去重逻辑.
	if int8(req.Source) == model.SourceAlarm {
		return nil, errorx.NewError(errorx.ErrBadRequest, "告警来源工单由消费者自动创建, 不支持人工指定")
	}

	task := &model.DispatchTask{
		TaskNo:   model.NewTaskNo(),
		Title:    req.Title,
		Source:   model.SourceManual,
		ZoneCode: req.ZoneCode,
		// 归一化技能标签, 保证与人员池中的写法能匹配上(大小写/空格/重复都抹平)
		RequiredSkill: model.NormalizeSkills(req.RequiredSkill),
		Priority:      int8(req.Priority),
		Status:        model.StatusPendingAssign,
		Description:   req.Description,
	}

	if err := l.svcCtx.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&model.DispatchTaskLog{
			TaskId:     task.Id,
			FromStatus: 0,
			ToStatus:   model.StatusPendingAssign,
			Action:     state.ActionCreate,
			Remark:     "人工创建",
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error
	}); err != nil {
		l.Errorf("[dispatch] create task failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "创建调度工单失败")
	}

	return &types.TaskCreateResp{
		Id:     task.Id,
		TaskNo: task.TaskNo,
		Status: int32(task.Status),
	}, nil
}
