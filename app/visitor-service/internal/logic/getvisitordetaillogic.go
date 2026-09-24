package logic

import (
	"context"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/rbac"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// GetVisitorDetailLogic 访客详情逻辑(P2): 详情 + 门禁进出轨迹(签入开门/签出动作留痕).
// 架构约束(docs/m3/03): M3(门禁)与 M2(访客)无直接调用 —— 访客轨迹以 visitor_record 自身的
// 门禁动作字段(checkin_at/device_id/checkout_at)为准, 不跨库查询 access_db.access_record.
type GetVisitorDetailLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetVisitorDetailLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetVisitorDetailLogic {
	return &GetVisitorDetailLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetVisitorDetail 返回访客详情与按时间正序的进出轨迹.
// 入参: req.Id 访客记录ID.
// 返回: 详情载荷 + track 轨迹(邀请生成码 → 签入/门禁开门 → 签出; 过期访客附过期失效节点).
// RBAC: 与列表口径一致 —— 物业等全量角色可见, 业主仅可见本人邀请的访客, 其余角色拒绝.
func (l *GetVisitorDetailLogic) GetVisitorDetail(req *types.VisitorIdReq) (resp *types.VisitorDetailResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)

	// 防御: 部署环境未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic.
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}
	if req.Id <= 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "访客记录ID非法")
	}

	q := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=?", req.Id, tenantID)

	// RBAC 行级隔离(与 ListVisitors 同口径): 业主仅能查看自己发起的邀请, 其余受限角色拒绝.
	uid := ctxdata.GetUserId(l.ctx)
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if !rbac.IsFullScope(roles) {
		if rbac.HasRole(roles, rbac.RoleOwner) {
			q = q.Where("inviter_id=?", uid)
		} else {
			q = q.Where("1=0")
		}
	}

	var rec model.VisitorRecord
	if e := q.First(&rec).Error; e != nil {
		if e == gorm.ErrRecordNotFound {
			return nil, errorx.NewError(errorx.ErrVisitorNotFound, "访客记录不存在")
		}
		l.Errorf("get visitor detail failed: %v", e)
		return nil, errorx.NewError(errorx.ErrM2Internal, "查询访客详情失败")
	}

	return &types.VisitorDetailResp{
		Id:           rec.ID,
		InviterId:    rec.InviterID,
		VisitorName:  rec.VisitorName,
		VisitorPhone: rec.VisitorPhone,
		Status:       rec.Status,
		VisitTime:    timeOrZero(rec.VisitTime),
		ExpireTime:   timeOrZero(rec.ExpireTime),
		CheckinAt:    timeOrZero(rec.CheckinAt),
		CheckoutAt:   timeOrZero(rec.CheckoutAt),
		DeviceId:     rec.DeviceID,
		Track:        buildVisitorTrack(&rec),
	}, nil
}

// buildVisitorTrack 从访客记录的门禁动作字段构建按时间正序的进出轨迹.
// 轨迹节点: 邀请生成二维码(必有) → 签入+门禁开门(已签入/已签出) → 签出(已签出) → 过期失效(已过期).
// device_id 为空表示签入时 M1 未配置/开门降级(签入成功但未留开门设备).
func buildVisitorTrack(rec *model.VisitorRecord) []types.VisitorTrackItem {
	track := make([]types.VisitorTrackItem, 0, 3)

	track = append(track, types.VisitorTrackItem{
		Action: "invite",
		Time:   rec.CreatedAt.Unix(),
		Remark: "邀请生成通行二维码",
	})

	if rec.CheckinAt != nil {
		remark := "扫码签入"
		if rec.DeviceID != "" {
			remark = "扫码签入, 门禁开门成功"
		} else {
			remark = "扫码签入, 开门设备未留痕(M1 未配置或开门降级)"
		}
		track = append(track, types.VisitorTrackItem{
			Action:   "checkin_open_door",
			Time:     rec.CheckinAt.Unix(),
			DeviceId: rec.DeviceID,
			Remark:   remark,
		})
	}

	if rec.CheckoutAt != nil {
		track = append(track, types.VisitorTrackItem{
			Action: "checkout",
			Time:   rec.CheckoutAt.Unix(),
			Remark: "签出离园",
		})
	}

	if rec.Status == model.VisitorStatusExpired {
		// 过期节点取二维码过期时间; ExpireTime 未设置(理论不会出现, 过期态必然有 expire_time)时兜底用 updated_at.
		expiredAt := rec.UpdatedAt
		if rec.ExpireTime != nil {
			expiredAt = *rec.ExpireTime
		}
		track = append(track, types.VisitorTrackItem{
			Action: "expired",
			Time:   expiredAt.Unix(),
			Remark: "二维码过期失效",
		})
	}

	return track
}
