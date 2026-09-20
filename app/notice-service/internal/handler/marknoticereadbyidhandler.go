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

// MarkNoticeReadByIdHandler 公告已读回填 HTTP 处理器(路径参数版别名).
// 路由: POST /api/notice/:id/read — 与 POST /api/notices/read(body 版)同语义同逻辑,
// 仅为评审要求的 RESTful 路径风格别名, 复用 MarkNoticeReadLogic 幂等回填.
func MarkNoticeReadByIdHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1) 解析路径参数 id 为公告ID(与项目其他 path 参数 handler 同惯用法: httpx.Parse + path 标签).
		var pathReq struct {
			Id int64 `path:"id"`
		}
		if err := httpx.Parse(r, &pathReq); err != nil || pathReq.Id <= 0 {
			response.Fail(w, errorx.NewError(errorx.ErrBadRequest, "公告ID不合法"))
			return
		}

		// 2) 复用已读回填逻辑(仅本人未读记录, 幂等).
		l := logic.NewMarkNoticeReadLogic(r.Context(), svcCtx)
		resp, err := l.MarkNoticeRead(&types.MarkNoticeReadReq{NoticeId: pathReq.Id})
		if err != nil {
			// 3) 失败: 业务错误码透出, 其余归 M2 兜底.
			if ce, ok := err.(*errorx.CodeError); ok {
				response.Fail(w, ce)
			} else {
				response.FailWith(w, errorx.ErrM2Internal, err.Error())
			}
			return
		}
		response.Ok(w, resp)
	}
}
