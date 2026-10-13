package chunker

import (
	"strings"
	"testing"
)

const sample = `# M5 招商租赁

## 一、合同状态机

合同有四条转移边。待生效到生效中，生效中到已到期，生效中到已终止，还有一条是续签。
只有生效中的合同才计入入驻率与出账，所以"生效中到已到期"这条边必须真有。

## 二、每日定时任务

00:05 跑两个任务，顺序不能颠倒。

` + "```powershell\n# 这段代码块应该被剔除，不进切片\ngo run ./app/leasing-service\n```" + `

先自动续约，再自动到期。服务停机跨过 00:05 时需要启动补偿。

## 三、三层防重复

Redis SET NX 锁按日期，10 分钟 TTL；更新条件里带 status 和 version；审计流水只在 RowsAffected>0 时写。
`

func TestSplit_标题路径与内容(t *testing.T) {
	chunks := Split(sample, DefaultOptions())
	if len(chunks) < 3 {
		t.Fatalf("至少应切出 3 片, 实际 %d", len(chunks))
	}
	// 每片都要带标题路径, 引用时才能说清出处
	for _, c := range chunks {
		if c.Heading == "" {
			t.Errorf("切片 %d 缺标题路径: %q", c.Seq, c.Content[:min(20, len(c.Content))])
		}
	}
	joined := ""
	for _, c := range chunks {
		joined += c.Content
	}
	for _, want := range []string{"生效中到已到期", "启动补偿", "RowsAffected"} {
		if !strings.Contains(joined, want) {
			t.Errorf("切片里丢了内容: %s", want)
		}
	}
}

func TestSplit_剔除代码块(t *testing.T) {
	chunks := Split(sample, DefaultOptions())
	for _, c := range chunks {
		if strings.Contains(c.Content, "go run ./app/leasing-service") {
			t.Fatalf("代码块应被剔除, 但出现在切片 %d", c.Seq)
		}
	}
}

func TestSplit_序号连续(t *testing.T) {
	chunks := Split(sample, DefaultOptions())
	for i, c := range chunks {
		if c.Seq != i {
			t.Fatalf("序号应连续: 第 %d 片 seq=%d", i, c.Seq)
		}
	}
}

func TestSplit_超长段落按句硬拆(t *testing.T) {
	long := strings.Repeat("这是一句用来测试硬拆的完整句子。", 60) // 约 900 字
	chunks := Split(long, Options{MaxLen: 200, MinLen: 10})
	if len(chunks) < 4 {
		t.Fatalf("900 字按 200 字上限应至少 4 片, 实际 %d", len(chunks))
	}
	for _, c := range chunks {
		if n := len([]rune(c.Content)); n > 260 {
			t.Errorf("切片超长未拆: %d 字", n)
		}
	}
}

func TestSplit_过短碎片丢弃(t *testing.T) {
	chunks := Split("## 标题\n\n短。\n\n## 另一个标题\n\n这又是一段很短的话。", Options{MaxLen: 400, MinLen: 30})
	if len(chunks) != 0 {
		t.Fatalf("都是短碎片, 应全部丢弃, 实际 %d 片", len(chunks))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
