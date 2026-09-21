package lease

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/leasing-service/internal/model"
	"onepark/app/leasing-service/internal/state"
	"onepark/app/leasing-service/internal/svc"
	"onepark/app/leasing-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// ContractUpdateLogic 更新合同: 续签(renew) / 终止(terminate) / 变更(update).
//
// 所有状态变更必须先过 state.Next 校验合法转移, 禁止在业务代码里散写 if status == xxx.

// actionUpdate 合同信息变更动作(不改变状态).
//
// 刻意不放进 state 包: 它不是一条状态转移边, 合法性由"是否终态"决定而非转移表。
const actionUpdate = "update"
type ContractUpdateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewContractUpdateLogic 构造合同更新逻辑.
func NewContractUpdateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ContractUpdateLogic {
	return &ContractUpdateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ContractUpdate 执行合同状态流转或信息变更, 并用乐观锁防并发覆盖.
func (l *ContractUpdateLogic) ContractUpdate(req *types.ContractUpdateReq) (*types.ContractUpdateResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	var contract model.LeaseContract
	err := l.svcCtx.DB.WithContext(l.ctx).First(&contract, req.Id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(errorx.ErrNotFound, "合同不存在")
	}
	if err != nil {
		l.Errorf("[lease] load contract failed: %v", err)
		return nil, errorx.NewError(errorx.ErrInternal, "加载合同失败")
	}

	updates := map[string]interface{}{"version": gorm.Expr("version+1")}
	nextStatus := contract.Status

	switch req.Action {
	case state.ActionActivate:
		// 人工生效: 不等定时任务, 允许提前把待生效合同置为生效中.
		to, ok := state.Next(contract.Status, state.ActionActivate)
		if !ok {
			return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许生效")
		}
		updates["status"] = to
		nextStatus = to

	case state.ActionRenew:
		to, ok := state.Next(contract.Status, state.ActionRenew)
		if !ok {
			return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许续签")
		}
		newEnd, perr := time.ParseInLocation(dateLayout, req.NewEndDate, time.Local)
		if perr != nil {
			return nil, errorx.NewError(errorx.ErrBadRequest, "续签终止日格式应为 yyyy-MM-dd")
		}
		if !newEnd.After(contract.EndDate) {
			return nil, errorx.NewError(errorx.ErrBadRequest, "续签终止日必须晚于原终止日")
		}
		updates["end_date"] = newEnd
		updates["status"] = to
		nextStatus = to
		if req.MonthlyRent != "" {
			rent, rerr := decimal.NewFromString(req.MonthlyRent)
			if rerr != nil || rent.IsNegative() {
				return nil, errorx.NewError(errorx.ErrBadRequest, "月租金格式非法")
			}
			updates["monthly_rent"] = rent
		}

	case state.ActionTerminate:
		to, ok := state.Next(contract.Status, state.ActionTerminate)
		if !ok {
			return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许终止")
		}
		updates["status"] = to
		nextStatus = to

	case state.ActionExpire:
		// 人工到期: 正常由定时任务每日自动执行, 此入口用于补偿处理与联调测试.
		to, ok := state.Next(contract.Status, state.ActionExpire)
		if !ok {
			return nil, errorx.NewError(errorx.ErrBadRequest, "当前状态不允许到期")
		}
		updates["status"] = to
		nextStatus = to

	case actionUpdate:
		// 信息变更不改变状态, 但终态合同不允许再改.
		if state.IsTerminal(contract.Status) {
			return nil, errorx.NewError(errorx.ErrBadRequest, "已终止的合同不可修改")
		}

		// AutoRenew / RenewNoticeDays 未传时为 -1(契约 default), 表示"本次不改动"
		changed := false
		if req.MonthlyRent != "" {
			rent, rerr := decimal.NewFromString(req.MonthlyRent)
			if rerr != nil || rent.IsNegative() {
				return nil, errorx.NewError(errorx.ErrBadRequest, "月租金格式非法")
			}
			updates["monthly_rent"] = rent
			changed = true
		}
		if req.AutoRenew >= 0 {
			if !validAutoRenew(req.AutoRenew) {
				return nil, errorx.NewError(errorx.ErrBadRequest, "auto_renew 仅支持 0(到期即止) / 1(自动续约)")
			}
			updates["auto_renew"] = int8(req.AutoRenew)
			changed = true
		}
		if req.RenewNoticeDays >= 0 {
			if !validNoticeDays(req.RenewNoticeDays) {
				return nil, errorx.NewError(errorx.ErrBadRequest, "renew_notice_days 应在 0~365 之间")
			}
			updates["renew_notice_days"] = req.RenewNoticeDays
			changed = true
		}
		if !changed {
			return nil, errorx.NewError(errorx.ErrBadRequest, "未提供需要变更的字段")
		}

	default:
		return nil, errorx.NewError(errorx.ErrBadRequest, "不支持的操作, 仅支持 activate/renew/expire/terminate/update")
	}

	// 乐观锁: 带 version 条件更新, RowsAffected=0 说明期间被他人改过.
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.LeaseContract{}).
		Where("id = ? AND version = ?", contract.Id, contract.Version).
		Updates(updates)
	if res.Error != nil {
		l.Errorf("[lease] update contract failed: %v", res.Error)
		return nil, errorx.NewError(errorx.ErrInternal, "更新合同失败")
	}
	if res.RowsAffected == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "合同已被他人修改, 请刷新后重试")
	}

	// 审计条件: 状态发生变化 **或** 合同条款发生实质变更。
	//
	// 为什么不能只看"状态是否变化": 续签是「生效中 → 生效中」(改了租期与租金, 但不改状态),
	// 若按状态变化判断, 人工续签将**完全不留下痕迹**。而同一件事由定时任务自动续约时
	// 是写审计的(internal/cron/daily.go 显式写 from=2 to=2) —— 两条路径两套标准。
	// update 同理: 改租金/续约条款不该改完就查无此事。
	if nextStatus != contract.Status || req.Action == state.ActionRenew || req.Action == actionUpdate {
		if err := l.svcCtx.DB.WithContext(l.ctx).Create(&model.LeaseContractStatusLog{
			ContractId: contract.Id,
			FromStatus: contract.Status,
			ToStatus:   nextStatus,
			Action:     req.Action,
			Reason:     req.Reason,
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error; err != nil {
			// 审计失败不影响主流程, 但必须留痕
			l.Errorf("[lease] write status log failed: contractId=%d, err=%v", contract.Id, err)
		}
	}

	return &types.ContractUpdateResp{Id: contract.Id, Status: int32(nextStatus)}, nil
}
