package lease

import (
	"context"
	"fmt"
	"math/rand"
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

// ContractCreateLogic 创建租赁合同.
// 新建合同初始状态固定为「待生效」, 到起租日由定时任务自动流转为「生效中」.
type ContractCreateLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewContractCreateLogic 构造创建合同逻辑.
func NewContractCreateLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ContractCreateLogic {
	return &ContractCreateLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ContractCreate 创建合同并写入初始状态审计.
func (l *ContractCreateLogic) ContractCreate(req *types.ContractCreateReq) (*types.ContractCreateResp, error) {
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "数据库未初始化")
	}

	monthlyRent, err := decimal.NewFromString(req.MonthlyRent)
	if err != nil || monthlyRent.IsNegative() {
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "月租金格式非法")
	}
	deposit := decimal.Zero
	if req.Deposit != "" {
		if deposit, err = decimal.NewFromString(req.Deposit); err != nil || deposit.IsNegative() {
			return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "押金格式非法")
		}
	}

	startDate, err := time.ParseInLocation(dateLayout, req.StartDate, time.Local)
	if err != nil {
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "起租日格式应为 yyyy-MM-dd")
	}
	endDate, err := time.ParseInLocation(dateLayout, req.EndDate, time.Local)
	if err != nil {
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "终止日格式应为 yyyy-MM-dd")
	}
	if !endDate.After(startDate) {
		return nil, errorx.NewError(ecode.ErrLeaseParamInvalid, "终止日必须晚于起租日")
	}
	if req.ZoneCode == "" {
		return nil, errorx.NewError(ecode.ErrZoneCodeInvalid, "区域编码不能为空")
	}

	from, _ := state.Next(0, state.ActionCreate)
	// 租户来源必须是网关注入的 x-tenant-id(经 IdentityFromHeader 写入 ctx),
	// 禁止信任请求体中的 tenant_id —— 否则客户端可伪造租户越权建合同.
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}
	contract := &model.LeaseContract{
		ContractNo:  newContractNo(),
		TenantId:    tenantID,
		TenantName:  req.TenantName,
		ZoneCode:    req.ZoneCode,
		AreaSqm:     req.AreaSqm,
		MonthlyRent: monthlyRent,
		Deposit:     deposit,
		StartDate:   startDate,
		EndDate:     endDate,
		Status:      model.StatusPending,
	}

	// 合同与审计流水必须同事务, 避免"有合同没流水"。
	err = l.svcCtx.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(contract).Error; err != nil {
			return err
		}
		return tx.Create(&model.LeaseContractStatusLog{
			ContractId: contract.Id,
			FromStatus: from,
			ToStatus:   model.StatusPending,
			Action:     state.ActionCreate,
			Reason:     "新建合同",
			OperatorId: ctxdata.GetUserId(l.ctx),
		}).Error
	})
	if err != nil {
		l.Errorf("[lease] create contract failed: %v", err)
		return nil, errorx.NewError(ecode.ErrContractCreateFailed, "创建合同失败")
	}

	return &types.ContractCreateResp{
		Id:         contract.Id,
		ContractNo: contract.ContractNo,
		Status:     int32(contract.Status),
	}, nil
}

// newContractNo 生成合同编号: LC + yyyyMMddHHmmss + 3 位随机数.
// 随机数用于降低同秒并发碰撞概率; 唯一性最终由 uk_contract_no 兜底.
func newContractNo() string {
	return fmt.Sprintf("LC%s%03d", time.Now().Format("20060102150405"), rand.Intn(1000))
}
