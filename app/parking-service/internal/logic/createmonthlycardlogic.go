package logic

import (
	"context"
	"strings"
	"time"

	"onepark/app/parking-service/internal/model"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// CreateMonthlyCardLogic 创建月卡逻辑(P2).
type CreateMonthlyCardLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateMonthlyCardLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateMonthlyCardLogic {
	return &CreateMonthlyCardLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CreateMonthlyCard 创建月卡: 车牌+有效期起止(+车主/电话).
// 校验: 车牌非空, 生效止晚于生效起; 同园区同车牌已存在"生效且未到期"月卡时拒绝重复办理.
// 表不建 (tenant,plate) 唯一键 —— 停用/过期后允许同车牌再次办卡, 识别只看 status+时间窗.
func (l *CreateMonthlyCardLogic) CreateMonthlyCard(req *types.CreateMonthlyCardReq) (resp *types.MonthlyCardResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	plateNo := strings.TrimSpace(req.PlateNo)
	if plateNo == "" {
		return nil, errorx.NewError(errorx.ErrBadRequest, "车牌号不能为空")
	}
	start, end := time.Unix(req.StartTime, 0), time.Unix(req.EndTime, 0)
	if !monthlyCardTimeValid(start, end) {
		return nil, errorx.NewError(errorx.ErrBadRequest, "生效止必须晚于生效起")
	}

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	// 同车牌有效月卡查重(仅拒"当前生效且未到期"的卡, 历史停用/过期卡不拦截).
	now := time.Now()
	var exists int64
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.MonthlyCard{}).
		Where("tenant_id=? AND plate_no=? AND status=? AND end_time>=?", tenantID, plateNo, model.MonthlyCardStatusActive, now).
		Count(&exists).Error; e != nil {
		l.Errorf("check monthly card exists failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询月卡失败")
	}
	if exists > 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "该车牌已存在生效月卡, 请直接续费")
	}

	card := &model.MonthlyCard{
		PlateNo:   plateNo,
		OwnerName: req.OwnerName,
		Phone:     req.Phone,
		StartTime: start,
		EndTime:   end,
		Status:    model.MonthlyCardStatusActive,
	}
	card.TenantID = tenantID
	card.CreatedAt = now
	card.UpdatedAt = now
	if e := l.svcCtx.DB.WithContext(l.ctx).Create(card).Error; e != nil {
		l.Errorf("create monthly card failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "创建月卡失败")
	}

	return monthlyCardResp(card), nil
}

// monthlyCardResp 月卡模型转响应(时间转秒级时间戳).
func monthlyCardResp(card *model.MonthlyCard) *types.MonthlyCardResp {
	return &types.MonthlyCardResp{
		Id:        card.ID,
		PlateNo:   card.PlateNo,
		OwnerName: card.OwnerName,
		Phone:     card.Phone,
		StartTime: card.StartTime.Unix(),
		EndTime:   card.EndTime.Unix(),
		Status:    card.Status,
	}
}

// fetchMonthlyCard 按 id+tenant 取月卡(公共小函数, 供续费/停用复用).
func fetchMonthlyCard(db *gorm.DB, ctx context.Context, tenantID, id int64) (*model.MonthlyCard, error) {
	var card model.MonthlyCard
	if e := db.WithContext(ctx).Where("id=? AND tenant_id=?", id, tenantID).First(&card).Error; e != nil {
		return nil, e
	}
	return &card, nil
}

// monthlyCardTimeValid 月卡有效期校验: 生效止必须晚于生效起.
// 抽为纯函数以便单测, 与 CreateMonthlyCard 业务约束保持一致.
func monthlyCardTimeValid(start, end time.Time) bool {
	return end.After(start)
}
