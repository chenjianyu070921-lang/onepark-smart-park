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

// ListDeadLettersHandler 死信台账分页列表(docs/m3/06 §5.4): 统一响应体 {code,msg,data}.
func ListDeadLettersHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListDLQReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListDeadLettersLogic(r.Context(), svcCtx)
		resp, err := l.ListDeadLetters(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmQuery))
			return
		}
		response.Ok(w, resp)
	}
}

// ReplayDeadLetterHandler 重放一条死信: 失败时返回 M3-E-1010, 台账状态保持待处理.
func ReplayDeadLetterHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewReplayDeadLetterLogic(r.Context(), svcCtx)
		resp, err := l.ReplayDeadLetter(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmDLQReplay))
			return
		}
		response.Ok(w, resp)
	}
}
