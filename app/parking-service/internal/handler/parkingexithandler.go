package handler

import (
	"net/http"

	"onepark/app/parking-service/internal/logic"
	"onepark/app/parking-service/internal/svc"
	"onepark/app/parking-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ParkingExitHandler 车辆离场计费 HTTP 处理器.
// 路由: POST /api/parking/exit (对外经网关: POST /api/parking/exit).
// 职责: 解析车牌/出场设备 -> 调 ParkingExitLogic(计费+结算+发离场/告警事件) -> 统一响应体.
func ParkingExitHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(车牌号/出场设备ID).
		var req types.ParkingExitReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行离场: 找在场记录 -> 计算时长/费用(CalcFee) -> 置已完成 -> 发 parking-exit(异常车另发 alarm-event).
		l := logic.NewParkingExitLogic(r.Context(), svcCtx)
		resp, err := l.ParkingExit(&req)
		if err != nil {
			// 3) 失败: 无在场记录等业务错误码透出(M2-E-3001), 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回记录ID/车牌/入离场时间/时长/费用/状态.
		response.Ok(w, resp)
	}
}
