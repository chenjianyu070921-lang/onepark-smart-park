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

// CreateRuleHandler 创建告警规则(#34): 统一响应体 {code,msg,data}.
func CreateRuleHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateRuleReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewCreateRuleLogic(r.Context(), svcCtx)
		resp, err := l.CreateRule(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmRuleCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// UpdateRuleHandler 更新 / 启用禁用规则(#35): 统一响应体 {code,msg,data}.
func UpdateRuleHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UpdateRuleReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewUpdateRuleLogic(r.Context(), svcCtx)
		resp, err := l.UpdateRule(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmRuleCreate))
			return
		}
		response.Ok(w, resp)
	}
}

// GetRuleHandler 规则详情(#36): 统一响应体 {code,msg,data}.
func GetRuleHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewGetRuleLogic(r.Context(), svcCtx)
		resp, err := l.GetRule(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmQuery))
			return
		}
		response.Ok(w, resp)
	}
}

// ListRulesHandler 规则分页列表(#37): 统一响应体 {code,msg,data}.
func ListRulesHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.ListRulesReq
		if err := httpx.Parse(r, &req); err != nil {
			response.Fail(w, errorx.NewError(errorx.ErrAlarmParamInvalid, "请求参数解析失败"))
			return
		}

		l := logic.NewListRulesLogic(r.Context(), svcCtx)
		resp, err := l.ListRules(&req)
		if err != nil {
			response.Fail(w, toCodeError(err, errorx.ErrAlarmQuery))
			return
		}
		response.Ok(w, resp)
	}
}

// toCodeError 将业务错误转为统一错误码响应: 已是业务错误则原样返回(保留细分码).
func toCodeError(err error, fallback string) *errorx.CodeError {
	if ce, ok := err.(*errorx.CodeError); ok {
		return ce
	}
	return errorx.NewError(fallback, err.Error())
}
