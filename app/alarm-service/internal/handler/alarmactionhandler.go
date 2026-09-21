package handler

import (
	"net/http"

	"onepark/app/alarm-service/internal/logic"
	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// 契约文档 docs/m3/04 把确认与解决写成两条独立路径(#39 #40), 实现却收敛为单条
// /api/alarm/:id/status + action 入参。本文件补回契约路径, 让按 #39/#40 对接的
// 调用方无需再自行拼装 action; 两条入口共用同一 logic, 因此不存在行为分叉。
// 保留 /status 的原因: 已有调用方在用, 删掉属于破坏性变更。

// AckAlarmHandler #39 PUT /api/alarm/:id/ack — 确认告警(未处理→已确认).
func AckAlarmHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return alarmActionHandler(svcCtx, model.AlarmActionAck, errorx.ErrAlarmAck)
}

// ResolveAlarmHandler #40 PUT /api/alarm/:id/resolve — 解决告警(已确认→已解决).
// 解决成功后 logic 会生产 Kafka onepark.alarm.event 通知 M5 自动派单;
// 通知失败返回 M3-E-1008 而非"解决失败" —— 此时状态已落库, 重试应打在补偿上。
func ResolveAlarmHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return alarmActionHandler(svcCtx, model.AlarmActionResolve, errorx.ErrAlarmResolve)
}

// alarmActionHandler 构造"动作由路径决定"的告警状态流转处理器。
// fallbackCode 兜底非 errorx.CodeError 的错误; logic 当前只返回 CodeError,
// 保留兜底是为了不让未知错误穿透成没有码值的裸 500。
func alarmActionHandler(svcCtx *svc.ServiceContext, action, fallbackCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.AlarmActionReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}
		// 动作取路径而非请求体: 若允许 body 指定, "PUT /ack 却传 action=resolve"
		// 会让实际流转与调用意图相反; 状态机只进不退, 这类错误无法回滚。
		// 专用入参类型同样源于此 —— 见 types.AlarmActionReq 注释。
		statusReq := types.UpdateAlarmStatusReq{Id: req.Id, Action: action, Remark: req.Remark}

		l := logic.NewUpdateAlarmStatusLogic(r.Context(), svcCtx)
		resp, err := l.UpdateAlarmStatus(&statusReq)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
				return
			}
			// 按动作归属语义码: 不能把解决失败报成"确认失败"(M3-E-1004)。
			response.FailWith(w, fallbackCode, err.Error())
			return
		}
		response.Ok(w, resp)
	}
}
