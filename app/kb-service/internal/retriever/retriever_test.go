package retriever

import (
	"context"
	"testing"
)

func loaderOf(chunks []Chunk) Loader {
	return func(context.Context, uint64, int) ([]Chunk, error) { return chunks, nil }
}

func fixture() []Chunk {
	return []Chunk{
		{ID: 1, DocumentID: 1, Seq: 0, Title: "调度策略设计", Source: "docs/m5/03", Content: "工单超时没人接怎么办：超时重派会把工单释放回待指派，并保留审计流水。"},
		{ID: 2, DocumentID: 1, Seq: 1, Title: "调度策略设计", Source: "docs/m5/03", Content: "智能派单按园区、技能、负载三项打分，选得分最高的人。"},
		{ID: 3, DocumentID: 2, Seq: 0, Title: "招商租赁业务流程", Source: "docs/m5/04", Content: "合同状态机有四条边，续签是自环，租期顺延，状态保持生效中。"},
		{ID: 4, DocumentID: 2, Seq: 1, Title: "招商租赁业务流程", Source: "docs/m5/04", Content: "每日 00:05 先自动续约再自动到期，顺序不能颠倒。"},
	}
}

func TestRetrieve_命中排序(t *testing.T) {
	r := NewKeyword(loaderOf(fixture()), 0.1, 100)
	hits, err := r.Retrieve(context.Background(), 1, "工单超时没人接怎么处理", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("应至少命中一条")
	}
	if hits[0].Chunk.ID != 1 {
		t.Fatalf("最相关的应是切片 1(超时重派), 实际 %d", hits[0].Chunk.ID)
	}
	if hits[0].Score <= 0 || hits[0].Score > 1 {
		t.Fatalf("归一化分数应在 0~1, 实际 %v", hits[0].Score)
	}
	// 分数必须降序
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Fatalf("分数未降序: [%d]=%v > [%d]=%v", i, hits[i].Score, i-1, hits[i-1].Score)
		}
	}
}

func TestRetrieve_阈值过滤_没有依据就不返回(t *testing.T) {
	// 阈值拉到 0.9: 只有题面词几乎全命中才留 —— 用来验证"无依据 -> 空结果"
	r := NewKeyword(loaderOf(fixture()), 0.9, 100)
	hits, err := r.Retrieve(context.Background(), 1, "食堂今天中午吃什么", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("语料里没有食堂相关内容, 应返回空, 实际 %d 条", len(hits))
	}
}

func TestRetrieve_空语料不报错(t *testing.T) {
	r := NewKeyword(loaderOf(nil), 0.1, 100)
	hits, err := r.Retrieve(context.Background(), 1, "合同状态机", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("空语料应返回空, 实际 %d", len(hits))
	}
}

func TestRetrieve_空问题不返回(t *testing.T) {
	r := NewKeyword(loaderOf(fixture()), 0.1, 100)
	hits, _ := r.Retrieve(context.Background(), 1, "   ", 3)
	if len(hits) != 0 {
		t.Fatalf("空问题应返回空, 实际 %d", len(hits))
	}
}

func TestRetrieve_topK生效(t *testing.T) {
	r := NewKeyword(loaderOf(fixture()), 0.01, 100)
	hits, _ := r.Retrieve(context.Background(), 1, "合同 工单 派单 续约", 2)
	if len(hits) > 2 {
		t.Fatalf("topK=2 却返回 %d 条", len(hits))
	}
}

func TestTokenize(t *testing.T) {
	got := Tokenize("工单超时 reindex 3 次")
	want := map[string]bool{"工单": true, "单超": true, "超时": true, "reindex": true, "3": true}
	set := map[string]bool{}
	for _, t2 := range got {
		set[t2] = true
	}
	for w := range want {
		if !set[w] {
			t.Errorf("缺少词元 %q (实际 %v)", w, got)
		}
	}
	if set["工单超时"] {
		t.Error("不应产出 4 字长词元(中文按 2-gram)")
	}
}

func TestRetrieve_结果稳定可复现(t *testing.T) {
	r := NewKeyword(loaderOf(fixture()), 0.01, 100)
	a, _ := r.Retrieve(context.Background(), 1, "合同状态机 续签", 3)
	b, _ := r.Retrieve(context.Background(), 1, "合同状态机 续签", 3)
	if len(a) != len(b) {
		t.Fatal("两次检索结果条数不一致")
	}
	for i := range a {
		if a[i].Chunk.ID != b[i].Chunk.ID || a[i].Score != b[i].Score {
			t.Fatalf("第 %d 条结果不稳定: %v vs %v", i, a[i], b[i])
		}
	}
}
