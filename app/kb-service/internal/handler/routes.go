// 路由表. 契约以 kb.api 为准, 这里手写注册(未跑 goctl, 避免生成物覆盖手写注释).
package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"

	"onepark/app/kb-service/internal/svc"
	"onepark/common/health"
)

func RegisterHandlers(server *rest.Server, serverCtx *svc.ServiceContext) {
	server.AddRoutes(
		[]rest.Route{
			{Method: http.MethodPost, Path: "/api/kb/documents", Handler: CreateDocumentHandler(serverCtx)},
			{Method: http.MethodGet, Path: "/api/kb/documents", Handler: ListDocumentsHandler(serverCtx)},
			{Method: http.MethodDelete, Path: "/api/kb/documents/:id", Handler: DeleteDocumentHandler(serverCtx)},
			{Method: http.MethodPost, Path: "/api/kb/ask", Handler: AskHandler(serverCtx)},
			{Method: http.MethodGet, Path: "/api/kb/qa-logs", Handler: ListQaLogsHandler(serverCtx)},
			{Method: http.MethodPost, Path: "/api/kb/feedback", Handler: FeedbackHandler(serverCtx)},
			{Method: http.MethodGet, Path: "/health", Handler: health.Handler(nil, nil)},
		},
	)
}
