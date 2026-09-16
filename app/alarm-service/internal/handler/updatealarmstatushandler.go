package handler

import (
	"net/http"

	"onepark/app/alarm-service/internal/logic"
	"onepark/app/alarm-service/internal/svc"
	"onepark/app/alarm-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// UpdateAlarmStatusHandler 告警状态流转(确认/解决): 统一响应体 {code,msg,data}.
func UpdateAlarmStatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateAlarmStatusReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewUpdateAlarmStatusLogic(r.Context(), svcCtx)
		resp, err := l.UpdateAlarmStatus(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrAlarmAck, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
