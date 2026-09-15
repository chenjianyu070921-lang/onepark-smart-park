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

// GetWorkOrderHandler 工单详情 HTTP 处理器.
// 路由: GET /api/workorder/:id (对外经网关: GET /api/workorder/:id).
// 职责: 解析路径参数 id -> 调 GetWorkOrderLogic(按租户隔离查询) -> 统一响应体.
func GetWorkOrderHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析路径参数 :id.
		var req types.IdReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 查询详情(强制 tenant_id 过滤; 不存在返回 M2-E-1001).
		l := logic.NewGetWorkOrderLogic(r.Context(), svcCtx)
		resp, err := l.GetWorkOrder(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回工单完整详情.
		response.Ok(w, resp)
	}
}
