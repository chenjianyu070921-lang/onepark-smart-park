package handler

import (
	"net/http"

	"onepark/app/dashboard-service/internal/logic"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"
	"github.com/zeromicro/go-zero/rest/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EnergyCardHandler 大屏能耗卡片: 转发 M4 接口54(GetDailyReport)
func EnergyCardHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.EnergyCardReq
		if err := httpx.Parse(r, &req); err != nil {
			writeErr(w, err)
			return
		}

		l := logic.NewDashboardLogic(r.Context(), svcCtx)
		resp, err := l.EnergyCard(&req)
		if err != nil {
			// M4 的参数错误透传给前端, 其余(连不上/内部错误)按依赖故障处理
			if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
				response.FailWith(w, errorx.ErrBadRequest, st.Message())
			} else {
				response.FailWith(w, errorx.ErrDepConnect, "能耗数据服务不可用")
			}
			return
		}
		response.Ok(w, resp)
	}
}
