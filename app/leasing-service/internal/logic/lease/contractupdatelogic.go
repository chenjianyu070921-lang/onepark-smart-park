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
	"onepark/app/leasing-service/internal/ecode"
	"onepark/common/errorx"
)

// ContractUpdateLogic 更新合同: 续签(renew) / 终止(terminate) / 变更(update).
//
// 所有状态变更必须先过 state.Next 校验合法转移, 禁止在业务代码里散写 if status == xxx.
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
	err := l.svcCtx.DB.WithContext(l.ctx).
		Where("id = ? AND tenant_id = ?", req.Id, ctxdata.GetTenantId(l.ctx)).
		First(&contract).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.NewError(ecode.ErrContractNotFound, "合同不存在")
	}
	if err != nil {
		l.Errorf("[lease] load contract failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractQueryFailed, "加载合同失败")
	}

	updates := map[string]interface{}{"version": gorm.Expr("version+1")}
	nextStatus := contract.Status

	switch req.Action {
	case state.ActionActivate:
		// 人工生效: 不等定时任务, 允许提前把待生效合同置为生效中.
		to, ok := state.Next(contract.Status, state.ActionActivate)
		if !ok {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "当前状态不允许生效")
		}
		updates["status"] = to
		nextStatus = to

	case state.ActionRenew:
		to, ok := state.Next(contract.Status, state.ActionRenew)
		if !ok {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "当前状态不允许续签")
		}
		newEnd, perr := time.ParseInLocation(dateLayout, req.NewEndDate, time.Local)
		if perr != nil {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "续签终止日格式应为 yyyy-MM-dd")
		}
		if !newEnd.After(contract.EndDate) {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "续签终止日必须晚于原终止日")
		}
		updates["end_date"] = newEnd
		updates["status"] = to
		nextStatus = to
		if req.MonthlyRent != "" {
			rent, rerr := decimal.NewFromString(req.MonthlyRent)
			if rerr != nil || rent.IsNegative() {
				return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "月租金格式非法")
			}
			updates["monthly_rent"] = rent
		}

	case state.ActionTerminate:
		to, ok := state.Next(contract.Status, state.ActionTerminate)
		if !ok {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "当前状态不允许终止")
		}
		updates["status"] = to
		nextStatus = to

	case state.ActionExpire:
		// 人工到期: 正常由定时任务每日自动执行, 此入口用于补偿处理与联调测试.
		to, ok := state.Next(contract.Status, state.ActionExpire)
		if !ok {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "当前状态不允许到期")
		}
		updates["status"] = to
		nextStatus = to

	case "update":
		// 信息变更不改变状态, 但终态合同不允许再改.
		if state.IsTerminal(contract.Status) {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "已终止的合同不可修改")
		}
		if req.MonthlyRent != "" {
			rent, rerr := decimal.NewFromString(req.MonthlyRent)
			if rerr != nil || rent.IsNegative() {
				return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "月租金格式非法")
			}
			updates["monthly_rent"] = rent
		} else {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "未提供需要变更的字段")
		}

	default:
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "不支持的操作, 仅支持 activate/renew/expire/terminate/update")
	}

	// 乐观锁 + 审计流水同事务: 带 version 条件更新, RowsAffected=0 说明期间被他人改过;
	// 状态变化时补审计流水, 与主更新要么都成功要么都回滚, 防止审计断档.
	err = l.svcCtx.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.LeaseContract{}).
			Where("id = ? AND version = ?", contract.Id, contract.Version).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errContractConflict
		}
		if nextStatus != contract.Status {
			if e := tx.Create(&model.LeaseContractStatusLog{
				ContractId: contract.Id,
				FromStatus: contract.Status,
				ToStatus:   nextStatus,
				Action:     req.Action,
				Reason:     req.Reason,
				OperatorId: ctxdata.GetUserId(l.ctx),
			}).Error; e != nil {
				return e
			}
		}
		return nil
	})
	if errors.Is(err, errContractConflict) {
		return nil, errorx.NewError(ecode.ErrContractConflict, "合同已被他人修改, 请刷新后重试")
	}
	if err != nil {
		l.Errorf("[lease] update contract failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractUpdateFailed, "更新合同失败")
	}

	return &types.ContractUpdateResp{Id: contract.Id, Status: int32(nextStatus)}, nil
}

// errContractConflict 事务内哨兵错误: 乐观锁冲突(version 不匹配).
var errContractConflict = errors.New("contract version conflict")
