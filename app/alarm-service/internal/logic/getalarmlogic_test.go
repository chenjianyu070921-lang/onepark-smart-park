package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
)

// 本文件补齐 GetAlarm(#39 告警详情)的单测覆盖 —— 此前全仓无任何用例引用该 logic.

// newGetAlarmCtx 接收接口而非具体 fake: 便于用不同替身表达"命中/404/存储异常"三种分支.
func newGetAlarmCtx(store model.AlarmModel) *svc.ServiceContext {
	return &svc.ServiceContext{Alarms: store}
}

func fullAlarm() *model.Alarm {
	ackAt := time.Unix(1757736000, 0)
	a := &model.Alarm{
		AlarmNo:   "AL20260917ab12cd34",
		RuleID:    7,
		DeviceID:  "door-01",
		AreaID:    12,
		EventType: "intrusion",
		Level:     model.AlarmLevelMajor,
		Status:    model.AlarmStatusAcked,
		Content:   "门禁设备检测到非法闯入",
		RequestID: "rid-1",
		AckBy:     9001,
		AckAt:     &ackAt,
	}
	// TenantID/CreatedAt/UpdatedAt 来自嵌入的 BaseModel, Go 不允许在复合字面量里赋值提升字段.
	a.TenantID = 10
	a.CreatedAt = time.Unix(1757735900, 0)
	a.UpdatedAt = time.Unix(1757736000, 0)
	return a
}

func TestGetAlarm_MissingTenant(t *testing.T) {
	l := NewGetAlarmLogic(context.Background(), newGetAlarmCtx(&fakeQueryStore{}))
	_, err := l.GetAlarm(&types.IdReq{Id: 1})
	if err == nil {
		t.Fatal("缺少租户应报错")
	}
	// KI-2: 参数校验类必须返回 M3-W-1001(HTTP 400), 返回 M6-E-0001 会变成 500.
	ce, ok := err.(*errorx.CodeError)
	if !ok || ce.Code != errorx.ErrAlarmParamInvalid {
		t.Errorf("应返回 M3-W-1001, 实际 %v", err)
	}
}

func TestGetAlarm_StorageNil(t *testing.T) {
	l := NewGetAlarmLogic(tenantCtx(10), &svc.ServiceContext{})
	_, err := l.GetAlarm(&types.IdReq{Id: 1})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrDepConnect {
		t.Errorf("存储未就绪应返回依赖错误, 实际 %v", err)
	}
}

func TestGetAlarm_Success(t *testing.T) {
	a := fullAlarm()
	a.ID = 321
	store := &fakeQueryStore{detail: a}
	l := NewGetAlarmLogic(tenantCtx(10), newGetAlarmCtx(store))

	resp, err := l.GetAlarm(&types.IdReq{Id: 321})
	if err != nil {
		t.Fatalf("查询详情失败: %v", err)
	}
	if resp.Id != 321 || resp.AlarmNo != a.AlarmNo || resp.RuleId != 7 {
		t.Errorf("详情字段映射异常: %+v", resp)
	}
	if resp.RequestId != "rid-1" || resp.AckBy != 9001 {
		t.Errorf("幂等键/确认人未透出: %+v", resp)
	}
	// ack_at 有值应转成秒级时间戳; resolve_at 为 nil 应转 0 而不是 panic.
	if resp.AckAt != 1757736000 {
		t.Errorf("ack_at 转换异常: %d", resp.AckAt)
	}
	if resp.ResolveAt != 0 {
		t.Errorf("未解决的告警 resolve_at 应为 0, 实际 %d", resp.ResolveAt)
	}
	if resp.Status != model.AlarmStatusAcked || resp.Level != model.AlarmLevelMajor {
		t.Errorf("状态/等级映射异常: %+v", resp)
	}
}

func TestGetAlarm_NotFound(t *testing.T) {
	l := NewGetAlarmLogic(tenantCtx(10), newGetAlarmCtx(&fakeQueryStore{}))
	_, err := l.GetAlarm(&types.IdReq{Id: 999})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmNotFound {
		t.Errorf("不存在应返回 M3-E-1003, 实际 %v", err)
	}
}

// getAlarmBrokenStore 复用 fakeQueryStore 的其余方法, 只改写 FindByID 以返回**非 404** 的存储错误.
// 不用 detailErr 的原因: 既有 fakeQueryStore 会把任意错误折叠成 ErrNotFound,
// 无法表达"连接被重置"与"记录不存在"的区别, 而这两者必须走不同分支.
type getAlarmBrokenStore struct {
	*fakeQueryStore
	err error
}

func (s *getAlarmBrokenStore) FindByID(context.Context, int64, int64) (*model.Alarm, error) {
	return nil, s.err
}

// TestGetAlarm_StoreError 存储异常应统一转成查询失败错误码, 不能伪装成"告警不存在":
// 二者语义完全不同(一个是数据缺失, 一个是依赖故障), 前端重试策略也不同.
func TestGetAlarm_StoreError(t *testing.T) {
	store := &getAlarmBrokenStore{fakeQueryStore: &fakeQueryStore{}, err: errors.New("connection reset")}
	l := NewGetAlarmLogic(tenantCtx(10), newGetAlarmCtx(store))
	_, err := l.GetAlarm(&types.IdReq{Id: 1})
	if ce, ok := err.(*errorx.CodeError); !ok || ce.Code != errorx.ErrAlarmQuery {
		t.Errorf("存储异常应返回 M3-E-1006, 实际 %v", err)
	}
}
