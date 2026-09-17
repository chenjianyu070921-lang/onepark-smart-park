package handler

import (
	"net/http"

	"onepark/app/access-control-service/internal/logic"
	"onepark/app/access-control-service/internal/svc"
	"onepark/app/access-control-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// ListAccessRecordsHandler 门禁通行记录分页查询 HTTP 处理器.
// 路由: GET /api/access/records (对外经网关: GET /api/access/records).
// 职责: 解析分页/点位过滤参数 -> 调 ListAccessRecordsLogic(强制租户隔离) -> 统一响应体.
func ListAccessRecordsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(page/page_size/gate_id).
		var req types.ListAccessRecordsReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行查询: tenant_id 强制隔离 + 可选点位过滤 + 分页.
		l := logic.NewListAccessRecordsLogic(r.Context(), svcCtx)
		resp, err := l.ListAccessRecords(&req)
		if err != nil {
			// 3) 失败: 查询失败透出 M3-E-2004, 其余归 M6-E-0005 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrInternal, err.Error())
			}
			return
		}

		// 4) 成功: 返回 total + 列表.
		response.Ok(w, resp)
	}
}
