package kb

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"onepark/app/kb-service/internal/config"
	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
	"onepark/common/ctxdata"
	"onepark/common/errorx"
)

// 测试用租户刻意与演示租户(1)分开: 本地演示与测试共用同一个 kb_db,
// 用独立租户 + defer 清理, 保证跑测试不会污染明天要演示的数据。
const testTenant = int64(990007001)

func newTestCtx(t *testing.T) (*svc.ServiceContext, context.Context) {
	t.Helper()
	dsn := os.Getenv("KB_MYSQL_DSN")
	if dsn == "" {
		t.Skip("KB_MYSQL_DSN 未设置, 跳过(与 M5 测试同一约定: 无库不做假绿)")
	}
	c := config.Config{}
	c.MySQL.DataSource = dsn
	c.Retrieval = config.RetrievalConf{Engine: "keyword", TopK: 3, MinScore: 0.5, MaxScan: 5000}
	svcCtx := svc.NewServiceContext(c)

	cleanup := func() {
		for _, tbl := range []string{"kb_feedback", "kb_qa_log", "kb_chunk", "kb_document"} {
			_ = svcCtx.DB.Exec("DELETE FROM "+tbl+" WHERE tenant_id = ?", testTenant).Error
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	ctx := ctxdata.SetTenantId(context.Background(), testTenant)
	ctx = ctxdata.SetUserId(ctx, 1)
	return svcCtx, ctx
}

const docTitle = "调度策略设计（测试）"
const docContent = `# 调度策略

## 超时重派
工单超时没人接时，超时重派会把工单释放回待指派状态，并保留审计流水。
释放与重派都用 Redis 锁加乐观锁，多实例并发下不会重复派单。

## 智能派单
智能派单按园区、技能、当前负载三项打分，选择得分最高且不在休假的人。
`

func seed(t *testing.T, svcCtx *svc.ServiceContext, ctx context.Context) {
	t.Helper()
	_, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{
		Title: docTitle, Source: "test/调度策略设计.md", Category: "流程", Content: docContent,
	})
	if err != nil {
		t.Fatalf("录入文档失败: %v", err)
	}
}

func TestFlow_录入覆盖重建切片(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)

	first, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{
		Title: docTitle, Source: "test/调度策略设计.md", Category: "流程", Content: docContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ChunkCount == 0 {
		t.Fatal("应切出切片")
	}
	// 同标题同出处再录一次 -> 覆盖, 切片数不翻倍
	second, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{
		Title: docTitle, Source: "test/调度策略设计.md", Category: "流程", Content: docContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Id != first.Id {
		t.Errorf("同材料的重复录入应是覆盖(同一文档 id), %d -> %d", first.Id, second.Id)
	}
	docs, chunks, err := svcCtx.KB.Stats(ctx, uint64(testTenant))
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Errorf("文档数应为 1, 实际 %d", docs)
	}
	if int64(second.ChunkCount) != chunks {
		t.Errorf("切片数应等于 chunk_count: %d vs %d", second.ChunkCount, chunks)
	}
}

func TestFlow_问答命中带引用(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	seed(t, svcCtx, ctx)

	resp, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: "工单超时没人接怎么办"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "extractive" && resp.Mode != "llm" {
		t.Fatalf("应命中并回答, 实际 mode=%s", resp.Mode)
	}
	if len(resp.Citations) == 0 {
		t.Fatal("命中时必须有引用出处")
	}
	if resp.Citations[0].Title != docTitle {
		t.Errorf("引用标题应为 %q, 实际 %q", docTitle, resp.Citations[0].Title)
	}
	if resp.QaId == 0 {
		t.Error("问答应落日志并返回 qaId(供反馈用)")
	}
	if !strings.Contains(resp.Answer, "超时重派") {
		t.Errorf("回答应包含语料里的原句, 实际:\n%s", resp.Answer)
	}
}

func TestFlow_无依据则拒答(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	seed(t, svcCtx, ctx)

	resp, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: "食堂今天中午吃什么"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "no_hit" {
		t.Fatalf("语料里没有的问题应拒答, 实际 mode=%s answer=%s", resp.Mode, resp.Answer)
	}
	if len(resp.Citations) != 0 {
		t.Error("拒答时不应给引用(会让人以为有依据)")
	}
}

func TestFlow_租户隔离(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	seed(t, svcCtx, ctx)

	// 换一个租户问同样的问题: 看不到别人的文档 -> 拒答
	other := ctxdata.SetTenantId(context.Background(), testTenant+1)
	resp, err := NewAskLogic(other, svcCtx).Ask(&types.AskRequest{Question: "工单超时没人接怎么办"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "no_hit" {
		t.Fatalf("跨租户不应命中, 实际 mode=%s", resp.Mode)
	}
}

func TestFlow_反馈与删除(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	seed(t, svcCtx, ctx)

	ask, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: "智能派单按什么打分"})
	if err != nil {
		t.Fatal(err)
	}
	// 正常投票
	if _, err := NewFeedbackLogic(ctx, svcCtx).Feedback(&types.FeedbackRequest{QaId: ask.QaId, Vote: 1, Comment: "有用"}); err != nil {
		t.Fatalf("投票应成功: %v", err)
	}
	// 给不存在的问答投票 -> 明确报错
	_, err = NewFeedbackLogic(ctx, svcCtx).Feedback(&types.FeedbackRequest{QaId: 99999999, Vote: 1})
	var ce *errorx.CodeError
	if !errors.As(err, &ce) || ce.Code != "M7-E-3905" {
		t.Fatalf("应返回 M7-E-3905, 实际 %v", err)
	}

	// 删除文档后再问 -> 拒答(切片已清理)
	docs, _, _ := svcCtx.KB.ListDocuments(ctx, uint64(testTenant), "", 1, 10)
	if len(docs) == 0 {
		t.Fatal("应有文档")
	}
	if _, err := NewDocumentLogic(ctx, svcCtx).Delete(&types.DeleteDocumentRequest{Id: int64(docs[0].ID)}); err != nil {
		t.Fatal(err)
	}
	after, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: "工单超时没人接怎么办"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode != "no_hit" {
		t.Errorf("文档删除后应拒答, 实际 mode=%s", after.Mode)
	}
}

func TestFlow_空问题与空正文(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	if _, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: "  "}); err == nil {
		t.Error("空问题应报错")
	}
	if _, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{Title: "", Content: "x"}); err == nil {
		t.Error("空标题应报错")
	}
	if _, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{Title: "x", Content: "   "}); err == nil {
		t.Error("空正文应报错")
	}
}
