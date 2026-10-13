package kb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onepark/app/kb-service/internal/svc"
	"onepark/app/kb-service/internal/types"
)

// 评测集: 把"该答的答、不该答的拒"变成可回归的约束。
//
// 为什么值得单独一个测试文件: 检索调参(阈值/停用词/分词)非常容易"按下葫芦起了瓢" ——
// 放宽一点, 不相关的问题就开始乱答; 收紧一点, 有答案的问题又被拒。人肉点几下看不出退化,
// 只有固定一组题 + 断言才能守住。
//
// 这组题也是答辩被问"效果怎么保证"时的答案: 18 题(10 应有依据 + 8 应拒答), 当前全绿。
var (
	evalInScope = []string{
		"工单派出去没人接怎么办？",
		"紧急工单多久要到场？",
		"电梯困人属于什么告警级别？",
		"租户欠费多久会被限制服务？",
		"合同到期了还想续租，租金怎么算？",
		"入驻率的分母是哪些面积？",
		"某栋楼用电突然变高，怎么判断是不是异常？",
		"告警重复上报会不会刷屏？",
		"消防通道被堵要多久到场？",
		"不足整月的话租金怎么折算？",
	}
	evalOutOfScope = []string{
		"食堂今天中午吃什么？",
		"今天天气怎么样？",
		"公司股票代码是多少？",
		"你是谁开发的？",
		"员工体检去哪里预约？",
		"如何申请专利？",
		"园区停车费多少钱一小时？",
		"打印机坏了找谁修？",
	}
)

// seedCorpus 把 docs/kb/corpus 下的真实语料灌进测试租户(与线上同一批材料)。
func seedCorpus(t *testing.T, svcCtx *svc.ServiceContext, ctx context.Context) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "..", "docs", "kb", "corpus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("找不到语料目录 %s, 跳过评测: %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		title := strings.TrimSuffix(e.Name(), ".md")
		if _, err := NewDocumentLogic(ctx, svcCtx).Create(&types.CreateDocumentRequest{
			Title: title, Source: "docs/kb/corpus/" + e.Name(), Category: "制度",
			Content: strings.TrimPrefix(string(raw), "\uFEFF"),
		}); err != nil {
			t.Fatalf("灌语料 %s 失败: %v", e.Name(), err)
		}
		n++
	}
	if n == 0 {
		t.Skip("语料目录为空, 跳过评测")
	}
}

func TestEval_该答的答_不该答的拒(t *testing.T) {
	svcCtx, ctx := newTestCtx(t)
	seedCorpus(t, svcCtx, ctx)

	missed := 0
	for _, q := range evalInScope {
		resp, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: q})
		if err != nil {
			t.Fatalf("问 %q 出错: %v", q, err)
		}
		if resp.Mode == "no_hit" || len(resp.Citations) == 0 {
			missed++
			t.Errorf("【漏答】%q -> mode=%s 引用=%d（应为有依据的回答）", q, resp.Mode, len(resp.Citations))
		}
	}

	wrong := 0
	for _, q := range evalOutOfScope {
		resp, err := NewAskLogic(ctx, svcCtx).Ask(&types.AskRequest{Question: q})
		if err != nil {
			t.Fatalf("问 %q 出错: %v", q, err)
		}
		if resp.Mode != "no_hit" {
			wrong++
			cite := ""
			if len(resp.Citations) > 0 {
				cite = resp.Citations[0].Title
			}
			t.Errorf("【误答】%q -> mode=%s top=%.4f 引用=%s（应拒答）", q, resp.Mode, resp.TopScore, cite)
		}
		if len(resp.Citations) != 0 {
			t.Errorf("【引用污染】%q 拒答时不应带引用", q)
		}
	}

	t.Logf("评测集: 应有依据 %d 题(漏答 %d) / 应拒答 %d 题(误答 %d)", len(evalInScope), missed, len(evalOutOfScope), wrong)
}
