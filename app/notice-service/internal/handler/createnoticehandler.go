package handler

import (
	"net/http"

	"onepark/app/notice-service/internal/logic"
	"onepark/app/notice-service/internal/svc"
	"onepark/app/notice-service/internal/types"
	"onepark/common/errorx"
	"onepark/common/response"

	"github.com/zeromicro/go-zero/rest/httpx"
)

// CreateNoticeHandler 发布公告 HTTP 处理器.
// 路由: POST /api/notice (对外经网关: POST /api/notice).
// 职责: 解析标题/正文/类型/定时发布 -> 调 CreateNoticeLogic(立即发布或草稿+发事件) -> 统一响应体.
func CreateNoticeHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析并校验参数(标题/正文/类型/是否置顶/定时发布时间).
		var req types.CreateNoticeReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		// 2) 执行发布: publish_at=0 立即发布(发 notice-event); 否则存草稿待调度发布.
		l := logic.NewCreateNoticeLogic(r.Context(), svcCtx)
		resp, err := l.CreateNotice(&req)
		if err != nil {
			// 3) 失败: 业务错误码透出(如缺租户 M6-E-0001), 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}

		// 4) 成功: 返回公告ID/标题/类型/状态/置顶/发布时间.
		response.Ok(w, resp)
	}
}
