package handler

import (
	"net/http"

	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/response"
)

// PingHandler 健康探针(K8s liveness/readiness 探活).
// 路由: GET /ping; 返回 200 {ok:true}, 不依赖任何外部组件, 仅判定进程是否存活.
func PingHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response.Ok(w, &types.PingResp{Ok: true})
	}
}
