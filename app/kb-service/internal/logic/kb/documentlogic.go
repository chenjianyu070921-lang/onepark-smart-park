// Package kb 知识库用例逻辑.
package kb

import (
	"context"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/kb-service/internal/chunker"
	"onepark/app/kb-service/internal/ecode"
	"onepark/app/kb-service/internal/model"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// DocumentLogic 文档接入与维护.
type DocumentLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDocumentLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DocumentLogic {
	return &DocumentLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// Create 录入文档: 切片 -> 落库(同租户同名同源 = 覆盖重建).
func (l *DocumentLogic) Create(req *types.CreateDocumentRequest) (*types.CreateDocumentResponse, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, errorx.NewError(ecode.ErrDocumentTitleEmpty, "文档标题不能为空")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errorx.NewError(ecode.ErrDocumentContentEmpty, "文档正文不能为空")
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = "未分类"
	}
	tenantID := uint64(ctxdata.GetTenantId(l.ctx))

	pieces := chunker.Split(content, chunker.DefaultOptions())
	if len(pieces) == 0 {
		return nil, errorx.NewError(ecode.ErrDocumentContentEmpty, "正文切不出有效片段(内容过短或全是代码块)")
	}
	chunks := make([]model.KbChunk, 0, len(pieces))
	for _, p := range pieces {
		chunks = append(chunks, model.KbChunk{
			Seq:     p.Seq,
			Heading: truncate(p.Heading, 200),
			Content: p.Content,
			CharLen: len([]rune(p.Content)),
		})
	}
	doc := &model.KbDocument{
		TenantID: tenantID,
		Title:    truncate(title, 200),
		Source:   truncate(strings.TrimSpace(req.Source), 300),
		Category: truncate(category, 64),
		Content:  content,
		Status:   1,
	}
	id, err := l.svcCtx.KB.SaveDocument(l.ctx, doc, chunks)
	if err != nil {
		l.Errorf("[kb] 文档入库失败: %v", err)
		return nil, errorx.NewError(ecode.ErrDocumentCreateFailed, err.Error())
	}
	l.Infof("[kb] 文档已入库 id=%d tenant=%d title=%q 切片=%d", id, tenantID, doc.Title, len(chunks))
	return &types.CreateDocumentResponse{Id: int64(id), ChunkCount: len(chunks)}, nil
}

// List 文档列表.
func (l *DocumentLogic) List(req *types.ListDocumentsRequest) (*types.ListDocumentsResponse, error) {
	page, size := normalizePage(req.Page, req.PageSize)
	tenantID := uint64(ctxdata.GetTenantId(l.ctx))
	docs, total, err := l.svcCtx.KB.ListDocuments(l.ctx, tenantID, strings.TrimSpace(req.Category), page, size)
	if err != nil {
		return nil, errorx.NewError(ecode.ErrDocumentCreateFailed, err.Error())
	}
	list := make([]types.DocumentItem, 0, len(docs))
	for _, d := range docs {
		list = append(list, types.DocumentItem{
			Id: int64(d.ID), Title: d.Title, Source: d.Source, Category: d.Category,
			ChunkCount: d.ChunkCount, Status: d.Status,
			CreatedAt: d.CreatedAt.Format(time.DateTime),
		})
	}
	return &types.ListDocumentsResponse{Total: total, List: list}, nil
}

// Delete 删除文档(软删文档 + 清切片).
func (l *DocumentLogic) Delete(req *types.DeleteDocumentRequest) (*types.DeleteDocumentResponse, error) {
	tenantID := uint64(ctxdata.GetTenantId(l.ctx))
	ok, err := l.svcCtx.KB.DocumentExists(l.ctx, tenantID, uint64(req.Id))
	if err != nil {
		return nil, errorx.NewError(ecode.ErrDocumentDeleteFailed, err.Error())
	}
	if !ok {
		return nil, errorx.NewError(ecode.ErrDocumentNotFound, "文档不存在")
	}
	n, err := l.svcCtx.KB.DeleteDocument(l.ctx, tenantID, uint64(req.Id))
	if err != nil {
		return nil, errorx.NewError(ecode.ErrDocumentDeleteFailed, err.Error())
	}
	l.Infof("[kb] 文档已删除 id=%d tenant=%d 清理切片=%d", req.Id, tenantID, n)
	return &types.DeleteDocumentResponse{Deleted: n}, nil
}

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
