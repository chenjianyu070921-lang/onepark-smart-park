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

// ListAlarmsHandler 告警列表: 统一响应体 {code,msg,data}.
func ListAlarmsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListAlarmsReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListAlarmsLogic(r.Context(), svcCtx)
		resp, err := l.ListAlarms(&req)
		if err != nil {
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrAlarmQuery, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
