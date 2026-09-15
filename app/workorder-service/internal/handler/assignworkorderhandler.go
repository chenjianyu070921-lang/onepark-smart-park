package handler

import (
	"net/http"

	"onepark/app/workorder-service/internal/logic"
	"onepark/app/workorder-service/internal/svc"
	"onepark/app/workorder-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// AssignWorkOrderHandler 派单 HTTP 处理器.
// 路由: PUT /api/workorder/:id/assign (对外经网关: PUT /api/workorder/:id/assign).
// 职责: 解析路径参数 id 与 body(处理人/部门) -> 调 AssignWorkOrderLogic(FSM+乐观锁) -> 统一响应体.
func AssignWorkOrderHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数: :id 走路径参数, assignee_id/department_id 走 JSON body.
		var req types.AssignWorkOrderReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行派单业务(状态机校验 + 乐观锁防并发 + 写流水 + 发 assigned 事件).
		l := logic.NewAssignWorkOrderLogic(r.Context(), svcCtx)
		resp, err := l.AssignWorkOrder(&req)
		if err != nil {
			// 3) 失败: 业务错误码原样透出(如 M2-E-1003 派单冲突), 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回工单ID/工单号/流转后状态.
		response.Ok(w, resp)
	}
}
