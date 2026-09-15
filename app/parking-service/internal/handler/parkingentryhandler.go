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

// ParkingEntryHandler 车辆入场 HTTP 处理器.
// 路由: POST /api/parking/entry (对外经网关: POST /api/parking/entry).
// 职责: 解析车牌/车型/入场设备 -> 调 ParkingEntryLogic(建在场记录+发入场事件) -> 统一响应体.
func ParkingEntryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(车牌号/车型/入场设备ID).
		var req types.ParkingEntryReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行入场: 写 parking_record(status=停车中) 并发布 parking-entry 事件.
		l := logic.NewParkingEntryLogic(r.Context(), svcCtx)
		resp, err := l.ParkingEntry(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回记录ID/车牌/入场时间/状态.
		response.Ok(w, resp)
	}
}
