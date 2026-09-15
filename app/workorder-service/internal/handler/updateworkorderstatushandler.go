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

// UpdateWorkOrderStatusHandler 工单状态流转 HTTP 处理器.
// 路由: PUT /api/workorder/:id/status (对外经网关: PUT /api/workorder/:id/status).
// 职责: 解析 :id 与 action/remark -> 调 UpdateWorkOrderStatusLogic(FSM 流转) -> 统一响应体.
func UpdateWorkOrderStatusHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析参数: :id 路径参数, action(submit/approve/reject/close) 与 remark 走 body.
		var req types.UpdateWorkOrderStatusReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行流转: FSM 校验目标状态 -> 乐观锁更新 -> 终态写 finished_at -> 写流水 -> 发 status_changed 事件.
		l := logic.NewUpdateWorkOrderStatusLogic(r.Context(), svcCtx)
		resp, err := l.UpdateWorkOrderStatus(&req)
		if err != nil {
			// 3) 失败: 非法流转返回 M2-E-1002 等业务错误码, 其余归 M2 兜底.
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
