package logic

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"onepark/app/access-control-service/internal/model"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
	"onepark/common/rbac"
)

// maxGrantItems 单次授权条数上限(person × device), 防止误传大列表拖垮实例.
const maxGrantItems = 500

// GrantAccessLogic 门禁批量授权逻辑(docs/m3/04 #45): person × device 笛卡尔积写入 access_permission.
type GrantAccessLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGrantAccessLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GrantAccessLogic {
	return &GrantAccessLogic{ctx: ctx, svcCtx: svcCtx}
}

// GrantAccess 批量写入权限; 已存在的组合按新权限覆盖(幂等).
func (l *GrantAccessLogic) GrantAccess(req *types.GrantAccessReq) (*types.GrantAccessResp, error) {
	tenantID := ctxdata.GetTenantId(l.ctx)
	if tenantID == 0 {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, "缺少租户信息(x-tenant-id)")
	}

	// RBAC: 授权是权限提权操作, 仅系统管理员/园区管理员/保安可操作(见 common/rbac 角色约定),
	// 原实现仅校验 tenant/operator 非空, 任意登录用户可给自己授权开门(审查问题14).
	roles := rbac.ParseRoleIds(ctxdata.GetRoleIds(l.ctx))
	if !rbac.HasRole(roles, rbac.RoleSystemAdmin) && !rbac.HasRole(roles, rbac.RoleParkAdmin) && !rbac.HasRole(roles, rbac.RoleSecurity) {
		return nil, errorx.NewError(errorx.ErrForbidden, "无权限管理门禁授权")
	}
	if l.svcCtx.Permissions == nil {
		return nil, errorx.NewError(errorx.ErrDepConnect, "权限存储未就绪(MySQL 未配置)")
	}

	personIDs, deviceIDs, err := normalizeTargets(req.PersonIds, req.DeviceIds)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, err.Error())
	}
	if len(personIDs)*len(deviceIDs) > maxGrantItems {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid,
			"单次授权条数超过上限("+strconv.Itoa(maxGrantItems)+")")
	}

	timeWindow, err := marshalTimeWindow(req.TimeWindow)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, err.Error())
	}
	expireAt, err := parseExpireAt(req.ExpireAt)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessParamInvalid, err.Error())
	}

	var whitelist int8
	if req.Whitelist {
		whitelist = 1
	}

	now := time.Now()
	permissions := make([]*model.AccessPermission, 0, len(personIDs)*len(deviceIDs))
	for _, personID := range personIDs {
		for _, deviceID := range deviceIDs {
			p := &model.AccessPermission{
				PersonID:   personID,
				DeviceID:   deviceID,
				TimeWindow: timeWindow,
				Whitelist:  whitelist,
				Status:     model.PermissionStatusValid,
				ExpireAt:   expireAt,
			}
			p.TenantID = tenantID
			p.CreatedAt = now
			p.UpdatedAt = now
			permissions = append(permissions, p)
		}
	}

	granted, err := l.svcCtx.Permissions.Grant(l.ctx, permissions)
	if err != nil {
		return nil, errorx.NewError(errorx.ErrAccessGrant, "授权门禁权限失败")
	}
	return &types.GrantAccessResp{Granted: granted}, nil
}

// normalizeTargets 清洗并去重人员/设备列表: 去除空值与重复项, 任一列表为空视为参数非法.
func normalizeTargets(personIDs []int64, deviceIDs []string) ([]int64, []string, error) {
	persons := make([]int64, 0, len(personIDs))
	seenPerson := make(map[int64]bool, len(personIDs))
	for _, id := range personIDs {
		if id <= 0 || seenPerson[id] {
			continue
		}
		seenPerson[id] = true
		persons = append(persons, id)
	}

	devices := make([]string, 0, len(deviceIDs))
	seenDevice := make(map[string]bool, len(deviceIDs))
	for _, id := range deviceIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenDevice[id] {
			continue
		}
		seenDevice[id] = true
		devices = append(devices, id)
	}

	if len(persons) == 0 || len(devices) == 0 {
		return nil, nil, errors.New("person_ids 与 device_ids 不能为空")
	}
	return persons, devices, nil
}

// marshalTimeWindow 将时间段权限序列化为 JSON; nil 返回 nil(NULL, 表示全天).
// 校验: start/end 为 "HH:mm" 且 days 取值在 1-7, 非法组合提前拒绝而非写坏数据.
func marshalTimeWindow(w *types.TimeWindow) (*string, error) {
	if w == nil {
		return nil, nil
	}
	if _, err := parseHHMM(w.Start); err != nil {
		return nil, errors.New("time_window.start 需为补零的 HH:mm(00:00-23:59), 如 08:00")
	}
	if _, err := parseHHMM(w.End); err != nil {
		return nil, errors.New("time_window.end 需为补零的 HH:mm(00:00-23:59), 如 20:00")
	}
	if len(w.Days) == 0 {
		return nil, errors.New("time_window.days 不能为空")
	}
	for _, d := range w.Days {
		if d < 1 || d > 7 {
			return nil, errors.New("time_window.days 取值需为 1-7(1=周一)")
		}
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, errors.New("time_window 序列化失败")
	}
	s := string(raw)
	return &s, nil
}

// parseHHMM 严格校验 HH:mm(00:00-23:59).
// time.Parse("15:04", v) 会放行 "8:00" 这类少位输入, 故解析后再格式化比对,
// 强制调用方传入规范补零形式, 避免同一时间点在库里出现多种写法.
func parseHHMM(v string) (time.Time, error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return time.Time{}, err
	}
	if t.Format("15:04") != v {
		return time.Time{}, errors.New("需补零的 HH:mm 格式")
	}
	return t, nil
}

// parseExpireAt 解析过期时间(RFC3339); 空串返回 nil(长期有效).
func parseExpireAt(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// 过期时间必须晚于当前: 给已过期时间授权没有业务意义.
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, errors.New("expire_at 需为 RFC3339 格式")
	}
	if !at.After(time.Now()) {
		return nil, errors.New("expire_at 必须晚于当前时间")
	}
	return &at, nil
}
