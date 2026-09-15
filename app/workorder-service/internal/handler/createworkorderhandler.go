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

// CreateWorkOrderHandler 创建工单 HTTP 处理器.
// 路由: POST /api/workorder (对外经网关: POST /api/workorder).
// 职责: 解析校验请求体 -> 调用 CreateWorkOrderLogic -> 以统一响应体 {code,msg,data} 返回.
// 说明: 租户ID/发起人ID 由网关经 Header 注入, logic 层从 context 读取, handler 不解析身份.
func CreateWorkOrderHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验请求参数(JSON body); 参数不合法直接返回 400, 不进入业务逻辑.
		var req types.CreateWorkOrderReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 构造业务逻辑对象(注入请求 context 与全局依赖 DB/Redis/Kafka)并执行核心业务.
		l := logic.NewCreateWorkOrderLogic(r.Context(), svcCtx)
		resp, err := l.CreateWorkOrder(&req)
		if err != nil {
			// 3) 失败: 带业务错误码的 CodeError 原样返回; 其余统一归为 M2 内部兜底错误.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 统一信封 {code:"0", msg:"ok", data:resp}.
		response.Ok(w, resp)
	}
}
