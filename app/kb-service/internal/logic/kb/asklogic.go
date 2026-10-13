package kb

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/kb-service/internal/answer"
	"onepark/app/kb-service/internal/ecode"
	"onepark/app/kb-service/internal/model"
	"onepark/app/kb-service/internal/retriever"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// AskLogic 问答: 检索 -> 组织回答 -> 落日志.
type AskLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAskLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AskLogic {
	return &AskLogic{Logger: logx.WithContext(ctx), ctx: ctx, svcCtx: svcCtx}
}

// Ask 问答主流程.
//
// 每一步都可测、可解释 —— 答辩时按这个顺序讲:
//  1. 检索(Retriever 接口, 现在是关键词, 可换向量)
//  2. 组织回答(有 key 走大模型, 没 key/失败走抽取式; 都没命中 -> 拒答)
//  3. 落问答日志(mode/top_score/citations 都要存, 这是后续"效果评估"的数据源)
func (l *AskLogic) Ask(req *types.AskRequest) (*types.AskResponse, error) {
	start := time.Now()
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return nil, errorx.NewError(ecode.ErrQuestionEmpty, "问题不能为空")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = l.svcCtx.Config.Retrieval.TopK
	}

	tenantID := uint64(ctxdata.GetTenantId(l.ctx))
	userID := uint64(ctxdata.GetUserId(l.ctx))

	hits, err := l.svcCtx.Retriever.Retrieve(l.ctx, tenantID, question, topK)
	if err != nil {
		l.Errorf("[kb] 检索失败: %v", err)
		return nil, errorx.NewError(ecode.ErrRetrieveFailed, err.Error())
	}

	res := l.svcCtx.Answer.Build(l.ctx, question, hits)

	// 引用片段围绕"命中的实词"取窗口, 而不是从段首截 200 字 —— 用户点开引用要看到关键句。
	terms := retriever.ContentTerms(question)
	citations := make([]types.Citation, 0, len(hits))
	for _, h := range hits {
		citations = append(citations, types.Citation{
			ChunkId:    int64(h.Chunk.ID),
			DocumentId: int64(h.Chunk.DocumentID),
			Title:      h.Chunk.Title,
			Source:     h.Chunk.Source,
			Seq:        h.Chunk.Seq,
			Score:      h.Score,
			Snippet:    answer.Snippet(h.Chunk.Content, terms, 200),
		})
	}
	topScore := 0.0
	if len(hits) > 0 {
		topScore = hits[0].Score
	}
	elapsed := time.Since(start).Milliseconds()

	// 日志: 写失败不影响本次回答(用户已经拿到答案, 不该因为审计表抖动而报错)
	logID := l.writeQaLog(tenantID, userID, question, res.Text, res.Mode, citations, topScore, elapsed)

	l.Infof("[kb] 问答 tenant=%d mode=%s 命中=%d top=%.4f 耗时=%dms", tenantID, res.Mode, len(hits), topScore, elapsed)
	return &types.AskResponse{
		Answer:    res.Text,
		Mode:      res.Mode,
		QaId:      int64(logID),
		Citations: citations,
		TopScore:  topScore,
		ElapsedMs: elapsed,
	}, nil
}

func (l *AskLogic) writeQaLog(tenantID, userID uint64, q, a, mode string, cites []types.Citation, top float64, elapsed int64) uint64 {
	raw, _ := json.Marshal(cites)
	entry := &model.KbQaLog{
		TenantID: tenantID, UserID: userID,
		Question: truncate(q, 500), Answer: a, Mode: mode,
		Citations: string(raw), TopScore: top, ElapsedMs: elapsed,
	}
	id, err := l.svcCtx.KB.InsertQaLog(l.ctx, entry)
	if err != nil {
		l.Errorf("[kb] 问答日志写入失败: %v", err)
	}
	return id
}
