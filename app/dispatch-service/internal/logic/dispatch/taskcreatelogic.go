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

	// 初始状态由状态机给出(0=不存在 --create--> 待指派), 不在业务代码里硬编码:
	// 与下面审计流水的 ToStatus 必须同源, 否则两处一旦写得不一样就是静默的状态漂移。
	initialStatus, ok := state.Next(0, state.ActionCreate)
	if !ok {
		return nil, errorx.NewError(errorx.ErrInternal, "状态机缺少建单起点")
	}

	task := &model.DispatchTask{
		TaskNo: model.NewTaskNo(),
		Title:  req.Title,
		Source: model.SourceManual,
		// 租户只从 ctx 取(网关注入, 不可伪造)。
		// ⚠️ 必须写: 列表/详情/状态流转都按 ctx 的租户过滤(Where tenant_id = ?),
		// 不写就恒为 0 —— 网关注入非 0 租户时, 用户会**建完单立刻查不到自己的单**。
		TenantID: ctxdata.GetTenantId(l.ctx),
		ZoneCode: req.ZoneCode,
		// 归一化技能标签, 保证与人员池中的写法能匹配上(大小写/空格/重复都抹平)
		RequiredSkill: model.NormalizeSkills(req.RequiredSkill),
		Priority:      int8(req.Priority),
		Status:        initialStatus,
		Description:   req.Description,
	}

	// 单号撞号(跨实例并发)时**换号重试**, 而不是把失败抛给用户。
	// NewTaskNo 已保证同进程内每秒 1000 张以内不重复, 这层只兜跨实例/超量的极小概率 ——
	// 但必须有: 撞了就返回 500, 用户会看到"建单失败", 而其实只差换一个号。
	const maxTaskNoAttempts = 3
	var createErr error
	for attempt := 1; attempt <= maxTaskNoAttempts; attempt++ {
		task.Id = 0 // 上一轮失败不留残值
		createErr = l.svcCtx.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(task).Error; err != nil {
				return err
			}
			return tx.Create(&model.DispatchTaskLog{
				TenantID:   task.TenantID, // 审计也写租户: 与主表同源, 便于按租户查流水
				TaskId:     task.Id,
				FromStatus: 0, // 「不存在」: 与状态机的建单起点一致
				ToStatus:   initialStatus,
				Action:     state.ActionCreate,
				Remark:     "人工创建",
				OperatorId: ctxdata.GetUserId(l.ctx),
			}).Error
		})
		if createErr == nil {
			break
		}
		if !model.IsDuplicateKeyOn(createErr, "uk_task_no") {
			break
		}
		l.Errorf("[dispatch] 单号撞号, 换号重试(第 %d/%d 次): %s", attempt, maxTaskNoAttempts, task.TaskNo)
		task.TaskNo = model.NewTaskNo()
	}
	if createErr != nil {
		l.Errorf("[dispatch] create task failed: %v", createErr)
		return nil, errorx.NewError(errorx.ErrInternal, "创建调度工单失败")
	}

	return &types.TaskCreateResp{
		Id:     task.Id,
		TaskNo: task.TaskNo,
		Status: int32(task.Status),
	}, nil
}
