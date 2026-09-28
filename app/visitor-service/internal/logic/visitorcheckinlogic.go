package logic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"onepark/app/visitor-service/internal/model"
	"onepark/app/visitor-service/internal/svc"
	"onepark/app/visitor-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/redisx"
	commonpb "onepark/proto/common"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// m1OpenDoorTimeout M1 开门 gRPC 短超时(设计文档要求: 访客开门设短超时, 失败明确提示).
const m1OpenDoorTimeout = 800 * time.Millisecond

// VisitorCheckinLogic 访客签入逻辑: 支持多方式核验(二维码/手机号/身份证/人脸/工牌) +
// 黑名单实时拦截 + 核销二维码 + 触发 M1 开门(gRPC SendCommand).
type VisitorCheckinLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewVisitorCheckinLogic(ctx context.Context, svcCtx *svc.ServiceContext) *VisitorCheckinLogic {
	return &VisitorCheckinLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// VisitorCheckin 处理访客签入: 多方式核验 + 黑名单拦截 + 核销 + 调 M1 开门.
// 入参: req.VerifyChannel 核验方式(0/1=二维码, 2=手机号, 3=身份证, 4=人脸, 5=工牌);
//
//	二维码方式需 QRCode, 其余方式需 Credential(对应维度凭证值).
//
// 返回: 记录ID/状态/签入时间/开门设备ID.
// 降级: M1 不可达或未配置时跳过开门, 签入仍然成功(device_id 保持为空).
func (l *VisitorCheckinLogic) VisitorCheckin(req *types.VisitorCheckinReq) (resp *types.VisitorCheckinResp, err error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrBadRequest, "缺少租户信息(x-tenant-id)")
	}

	// 防御: 未配置 MySQL 时 svcCtx.DB 为 nil, 提前返回明确错误避免空指针 panic(审查问题12).
	if l.svcCtx.DB == nil {
		return nil, errorx.NewError(errorx.ErrM2Internal, "数据库未初始化")
	}

	channel := req.VerifyChannel
	if channel == 0 {
		channel = 1 // 默认二维码核验
	}

	var rec model.VisitorRecord
	if channel == 1 {
		// 二维码核验: 验签(防伪造) + 按 qr_code 查找.
		if req.QRCode == "" {
			return nil, errorx.NewError(errorx.ErrBadRequest, "二维码核验需提供 qr_code")
		}
		if e := verifyQRSign(req.QRCode, l.svcCtx.Config.QrSignSalt); e != nil {
			l.Errorf("verify qr sign failed: %v", e)
			return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码无效")
		}
		if e := l.svcCtx.DB.WithContext(l.ctx).Where("qr_code=? AND tenant_id=?", req.QRCode, tenantID).First(&rec).Error; e != nil {
			if e == gorm.ErrRecordNotFound {
				return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码无效")
			}
			l.Errorf("load visitor record failed: %v", e)
			return nil, errorx.NewError(errorx.ErrM2Internal, "加载访客记录失败")
		}
		if rec.VerifyChannel != 1 {
			return nil, errorx.NewError(errorx.ErrVisitorVerifyChannel, "该记录非二维码核验, 请用对应方式签入")
		}
	} else {
		// 多方式核验: 按核验维度字段查找(手机号/身份证/人脸/工牌), 跳过二维码签名校验.
		if req.Credential == "" {
			return nil, errorx.NewError(errorx.ErrBadRequest, "非二维码核验需提供 credential")
		}
		col, e := verifyChannelColumn(channel)
		if e != nil {
			return nil, e
		}
		// 仅匹配"待使用"且最近的一条记录(最近优先): 同凭证存在多条邀请(如重复邀请)时,
		// 原 First(按主键升序)会误绑最早/已消费记录, 导致签入错绑或误报已核销(审查建议: 多方式签入 First 绑定歧义).
		if e := l.svcCtx.DB.WithContext(l.ctx).
			Where(col+"=? AND tenant_id=? AND status=?", req.Credential, tenantID, model.VisitorStatusPending).
			Order("id DESC").First(&rec).Error; e != nil {
			if e == gorm.ErrRecordNotFound {
				return nil, errorx.NewError(errorx.ErrVisitorVerifyChannel, "凭证无效或未找到待签到的访客记录")
			}
			l.Errorf("load visitor record failed: %v", e)
			return nil, errorx.NewError(errorx.ErrM2Internal, "加载访客记录失败")
		}
		if rec.VerifyChannel != channel {
			return nil, errorx.NewError(errorx.ErrVisitorVerifyChannel, "核验方式不匹配该访客记录")
		}
	}

	// 黑名单实时拦截(P2): 命中则标记该记录并拒绝通行.
	// 安全设计: 查询异常 fail-closed(拒绝通行), 防止 DB 抖动期间黑名单被绕过(典型 fail-open 漏洞).
	blocked, e := checkBlocked(l.ctx, l.svcCtx, rec.TenantID, rec.VisitorPhone, rec.IdNo)
	if e != nil {
		// 黑名单查询异常: fail-closed 拒绝通行, 防止 DB 抖动期间黑名单被绕过;
		// 结构化日志带 tenant_id/visitor_id/手机号 上下文, 确保异常不被静默吞掉(审查清单①).
		logx.Errorw("访客黑名单查询异常, fail-closed 拒绝通行",
			logx.Field("tenant_id", tenantID),
			logx.Field("visitor_id", rec.ID),
			logx.Field("phone", rec.VisitorPhone),
			logx.Field("err", e.Error()))
		return nil, errorx.NewError(errorx.ErrVisitorBlocklistCheckFailed, "风控校验暂不可用，已临时拒绝通行")
	}
	if blocked {
		// 命中黑名单: 结构化日志带上下文, 确保命中路径也不静默(审查清单①两条路径都不吞错).
		logx.Infow("访客命中黑名单, 拒绝通行",
			logx.Field("tenant_id", tenantID),
			logx.Field("visitor_id", rec.ID),
			logx.Field("phone", rec.VisitorPhone))
		_ = l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
			Where("id=? AND tenant_id=?", rec.ID, tenantID).
			Updates(map[string]interface{}{"blacklisted": 1, "updated_at": time.Now()}).Error
		// 黑名单命中事件(看板 P2): blocked → Kafka visitor-event, 供安防/大屏实时感知拦截动态;
		// 尽力而为语义, 发布失败不影响拦截拒绝结果.
		publishVisitorEvent(l.ctx, l.svcCtx, l.Logger, buildBlockedEvent(
			tenantID, rec.ID, rec.InviterID, rec.VisitorName, rec.VisitorPhone, rec.Status))
		return nil, errorx.NewError(errorx.ErrVisitorBlacklisted, "访客已被拉黑, 禁止通行")
	}

	now := time.Now()
	// 过期校验: 超过过期时间置为已过期并拒绝签入.
	if rec.ExpireTime != nil && now.After(*rec.ExpireTime) {
		if rec.Status != model.VisitorStatusExpired {
			_ = l.svcCtx.DB.WithContext(l.ctx).Model(&rec).Updates(map[string]interface{}{"status": model.VisitorStatusExpired, "updated_at": now}).Error
		}
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeExpired, "二维码已过期")
	}
	// 已签入/已签出不允许重复核销.
	if rec.Status == model.VisitorStatusCheckin || rec.Status == model.VisitorStatusCheckout {
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}

	// 2) Redis SETNX 防重放/防并发双扫(在 DB CAS 之前的第一道闸, 与 DB CAS 形成双保险, 验证报告 F1).
	// key 按访客记录ID维度隔离, TTL 覆盖至二维码过期(无过期兜底 24h);
	// SETNX 成功=本请求获得核销权; 失败=窗口内已被核销(并发/重放), 直接拒绝.
	// Redis 不可达时降级跳过此闸, 退回 DB CAS 单一防护(与原行为一致), 不误伤正常签入.
	redeemKey := redeemLockKey(rec.ID)
	claimed, rErr := l.svcCtx.Redis.SetNX(l.ctx, redeemKey, "1", redeemTTL(rec.ExpireTime)).Result()
	if rErr == nil && !claimed {
		l.Infof("visitor redeem lock conflict, already redeemed rec_id=%d", rec.ID)
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}

	// 3) 核销并置为已签入(RBAC 隔离: 带 tenant_id 条件, CAS 防并发重复开门).
	res := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=? AND status=?", rec.ID, tenantID, model.VisitorStatusPending).
		Updates(map[string]interface{}{
			"status":     model.VisitorStatusCheckin,
			"checkin_at": now,
			"updated_at": now,
		})
	if e := res.Error; e != nil {
		// 核销失败: 释放 Redis 闸, 避免残留锁误伤后续正常签入.
		releaseRedeemLock(l.svcCtx.Redis, redeemKey)
		l.Errorf("visitor checkin failed: %v", e)
		return nil, errorx.NewError(errorx.ErrVisitorCheckinFailed, "签入失败")
	}
	if res.RowsAffected == 0 {
		// 并发下第二次扫码走到这里: 释放本 goroutine 的锁, 返回"已核销", 绝不重复开门.
		releaseRedeemLock(l.svcCtx.Redis, redeemKey)
		l.Infof("visitor checkin skipped, already consumed rec_id=%d", rec.ID)
		return nil, errorx.NewError(errorx.ErrVisitorQRCodeUsed, "二维码已被核销")
	}
	// 核销成功: 保留 redeemKey(TTL 内阻止同码重放), 进入开门流程.

	// 2) 调 M1 开门并把开门设备ID回填 visitor_record.device_id(失败降级, 不阻断签入).
	deviceID, openMsg := l.openDoor(rec.ID, tenantID)

	// 发布访客事件(评审 P1): checkin → Kafka visitor-event, 供大屏等消费方实时感知; 尽力而为不阻断.
	rec.Status = model.VisitorStatusCheckin
	rec.DeviceID = deviceID
	publishVisitorEvent(l.ctx, l.svcCtx, l.Logger, VisitorEvent{
		Event:        "checkin",
		TenantId:     rec.TenantID,
		VisitorId:    rec.ID,
		VisitorName:  rec.VisitorName,
		VisitorPhone: rec.VisitorPhone,
		InviterId:    rec.InviterID,
		Status:       rec.Status,
		DeviceId:     deviceID,
	})

	return &types.VisitorCheckinResp{
		Id:        rec.ID,
		Status:    model.VisitorStatusCheckin,
		CheckinAt: now.Unix(),
		DeviceID:  deviceID,
		OpenMsg:   openMsg,
	}, nil
}

// verifyChannelColumn 将核验方式枚举映射为 visitor_record 上的匹配字段.
// 2=手机号 3=身份证 4=人脸令牌 5=工牌号; 其它(含1二维码)返回错误.
func verifyChannelColumn(channel int8) (string, error) {
	switch channel {
	case 2:
		return "visitor_phone", nil
	case 3:
		return "id_no", nil
	case 4:
		return "face_token", nil
	case 5:
		return "badge_no", nil
	default:
		return "", errorx.NewError(errorx.ErrVisitorVerifyChannel, "不支持的核验方式(仅支持 2手机号/3身份证/4人脸/5工牌)")
	}
}

// checkBlocked 查询访客黑名单是否命中(人员维度实时拦截).
// 入参: 当前 ctx/svcCtx/tenantID, 以及待校验的手机号与身份证号(可空, 全空直接返回未命中).
// 返回: true 表示在生效期内被拉黑; 查询异常时返回 error, 安全关键调用方须 fail-closed(拒绝通行) 而非放行.
func checkBlocked(ctx context.Context, svcCtx *svc.ServiceContext, tenantID int64, phone, idNo string) (bool, error) {
	if phone == "" && idNo == "" {
		return false, nil
	}
	now := time.Now()
	var cnt int64
	q := svcCtx.DB.WithContext(ctx).Model(&model.VisitorBlocklist{}).
		Where("tenant_id = ? AND status = ?", tenantID, model.BlocklistStatusActive).
		Where("(effective_from IS NULL OR effective_from <= ?) AND (effective_to IS NULL OR effective_to >= ?)", now, now)
	conds := []string{}
	args := []interface{}{}
	if phone != "" {
		conds = append(conds, "phone = ?")
		args = append(args, phone)
	}
	if idNo != "" {
		conds = append(conds, "id_no = ?")
		args = append(args, idNo)
	}
	q = q.Where(strings.Join(conds, " OR "), args...)
	if e := q.Count(&cnt).Error; e != nil {
		return false, e
	}
	return cnt > 0, nil
}

// openDoorResult 开门降级提示语(场景3 验收口径): 无论 M1 是否开门成功, 签入均不阻断,
// 降级时明确提示"请联系前台人工开门", 由前台兜底放行.
const (
	// openMsgSuccess 开门指令已下发提示.
	// 注意: device-service 的 SendCommand 为异步 MQTT 下行, 返回的 Success 仅表示"指令已受理下发",
	// 不代表物理门已开; 故文案由"开门成功"改为"开门指令已下发", 避免向前台/访客虚高确认(验证报告 F2).
	openMsgSuccess  = "开门指令已下发"
	openMsgDegrade  = "门禁未响应，签入已记录，请联系前台人工开门"
	openMsgNoConfig = "门禁未配置，签入已记录，请联系前台人工开门"
)

// openDoorMsg 开门结果提示语构造(纯函数, 便于降级策略单测).
// 入参: configured M1 开门链路是否已配置(DeviceRPC+门岗设备); opened 是否成功下发开门指令.
// 说明: opened 仅代表"开门指令已下发(device-service 受理)", 不等同物理门已开(异步 MQTT, 见 F2).
func openDoorMsg(configured, opened bool) string {
	switch {
	case opened:
		return openMsgSuccess
	case !configured:
		return openMsgNoConfig
	default:
		return openMsgDegrade
	}
}

// redeemLockKey 访客核销防重放 Redis 锁 key(按记录ID维度隔离, 与 DB CAS 的 tenant_id 隔离互补).
func redeemLockKey(recID int64) string {
	return fmt.Sprintf("visitor:redeem:%d", recID)
}

// redeemTTL 核销锁有效期: 覆盖至二维码过期时间(无过期则兜底 24h),
// 既保证一次有效访问窗口内不可重放, 也避免过期后残留锁误伤后续同ID记录.
func redeemTTL(expire *time.Time) time.Duration {
	const fallback = 24 * time.Hour
	if expire == nil {
		return fallback
	}
	if d := time.Until(*expire); d > 0 {
		return d
	}
	return fallback
}

// releaseRedeemLock 释放核销锁(尽力而为: 失败仅记日志, 不影响主流程返回).
// 仅在 DB 核销失败/被并发抢先时调用, 防止 Redis 残留锁误伤正常访客.
func releaseRedeemLock(rdb *redisx.Client, key string) {
	if rdb == nil {
		return
	}
	if err := rdb.Del(context.Background(), key).Err(); err != nil {
		logx.Errorf("release visitor redeem lock failed, key=%s: %v", key, err)
	}
}

// openDoor 向 M1 device-service 下发开门指令, 并将实际开门设备ID回填到访客记录.
// 入参: recID 访客记录ID, tenantID 园区ID(回填时 RBAC 隔离).
// 返回: 回填的 device_id + 开门结果提示语; M1 未配置/不可达时 device_id 为空、
// 提示"请联系前台人工开门"(降级不阻断签入, 超时由 m1OpenDoorTimeout 短超时控制).
func (l *VisitorCheckinLogic) openDoor(recID, tenantID int64) (string, string) {
	door := l.svcCtx.Config.Door
	if l.svcCtx.DeviceRPC == nil || door.DeviceID == "" {
		l.Infof("skip M1 open door: device rpc or door device not configured (rec_id=%d)", recID)
		return "", openDoorMsg(false, false)
	}

	command := door.Command
	if command == "" {
		command = "open_door"
	}

	ctx, cancel := context.WithTimeout(l.ctx, m1OpenDoorTimeout)
	defer cancel()
	res, e := l.svcCtx.DeviceRPC.SendCommand(ctx, &commonpb.DeviceCommand{
		DeviceId: door.DeviceID,
		Command:  command,
	})
	if e != nil {
		// M1 不可达/超时: 降级, 不阻断访客签入, 明确提示人工兜底.
		l.Errorf("call M1 SendCommand failed, degrade checkin: %v", e)
		return "", openDoorMsg(true, false)
	}

	// 优先使用 M1 返回的实际开门设备ID, 缺省回落到配置的门岗设备ID.
	deviceID := door.DeviceID
	if res != nil && res.DeviceId != "" {
		deviceID = res.DeviceId
		l.Infof("M1 open door success: rec_id=%d device_id=%s", recID, deviceID)
	}

	// 回填 device_id.
	if e := l.svcCtx.DB.WithContext(l.ctx).Model(&model.VisitorRecord{}).
		Where("id=? AND tenant_id=?", recID, tenantID).
		Update("device_id", deviceID).Error; e != nil {
		l.Errorf("backfill visitor device_id failed: %v", e)
	}
	return deviceID, openDoorMsg(true, true)
}
