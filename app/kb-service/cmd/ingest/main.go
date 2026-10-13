// 语料灌入工具: 把"园区自己的材料"变成知识库内容.
//
// 灌两类:
//  1. 本项目的流程/技术文档(docs/m5/*.md) —— 演示问答时的语料主体;
//  2. 历史工单(workorder_db.work_order) —— "工单经验"这一类, 让知识库有业务味道:
//     问"机房温度过高怎么处置"能捞到真实工单的处置记录。
//
// 为什么不做成服务里的接口: 灌数据是**离线**动作(读文件系统、连另一个库),
// 放服务里会引入跨库连接与文件系统依赖; 而且"服务间一律 gRPC、不跨库"是项目硬约束,
// 这里用一次性命令行工具, 边界最干净。
//
// 幂等: 依赖 kb-service 的 SaveDocument(同一 tenant+title+source = 覆盖重建切片),
// 可以反复跑, 不会产生重复文档。
//
// 用法:
//
//	go run ./cmd/ingest -f etc/kb-api.yaml -dir ../../../docs/m5 -tenant 1
//	(workorder_db 的 DSN 走环境变量 WORKORDER_MYSQL_DSN; 不设则跳过工单那一类)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zeromicro/go-zero/core/conf"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"onepark/app/kb-service/internal/chunker"
	"onepark/app/kb-service/internal/config"
	"onepark/app/kb-service/internal/model"
)

var (
	configFile = flag.String("f", "etc/kb-api.yaml", "the config file")
	dir        = flag.String("dir", "../../../docs/m5", "documents dir (markdown)")
	tenant     = flag.Int64("tenant", 1, "tenant id")
	skipDocs   = flag.Bool("skip-docs", false, "skip markdown ingestion")
	skipWO     = flag.Bool("skip-workorders", false, "skip work order ingestion")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	db, err := gorm.Open(mysql.Open(c.MySQL.DataSource), &gorm.Config{})
	if err != nil {
		fmt.Printf("连 kb_db 失败: %v\n", err)
		os.Exit(1)
	}
	m := model.NewKBModel(db)

	totalDocs, totalChunks := 0, 0

	if !*skipDocs {
		d, ch, err := ingestDocs(ctx, m, *dir, *tenant)
		if err != nil {
			fmt.Printf("灌文档失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("文档: %d 篇 / %d 片\n", d, ch)
		totalDocs += d
		totalChunks += ch
	}

	if !*skipWO {
		d, ch, err := ingestWorkOrders(ctx, m, *tenant)
		if err != nil {
			fmt.Printf("灌工单失败(可忽略): %v\n", err)
		} else if d > 0 {
			fmt.Printf("工单经验: %d 条 / %d 片\n", d, ch)
			totalDocs += d
			totalChunks += ch
		}
	}

	docs, chunks, err := m.Stats(ctx, uint64(*tenant))
	if err != nil {
		fmt.Printf("统计失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("完成: 本次写入 %d 篇 / %d 片; 库内租户 %d 现有 %d 篇 / %d 片\n",
		totalDocs, totalChunks, *tenant, docs, chunks)
}

func ingestDocs(ctx context.Context, m *model.KBModel, dir string, tenant int64) (int, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)

	docs, chunks := 0, 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			fmt.Printf("  跳过 %s: %v\n", filepath.Base(f), err)
			continue
		}
		content := strings.TrimPrefix(string(raw), "\uFEFF") // 去 BOM, 否则首个标题切不出来
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		d := &model.KbDocument{
			TenantID: uint64(tenant),
			Title:    name,
			Source:   filepath.ToSlash(f),
			Category: categoryOf(name),
			Content:  content,
			Status:   1,
		}
		pieces := chunker.Split(content, chunker.DefaultOptions())
		list := make([]model.KbChunk, 0, len(pieces))
		for _, p := range pieces {
			list = append(list, model.KbChunk{Seq: p.Seq, Heading: p.Heading, Content: p.Content, CharLen: len([]rune(p.Content))})
		}
		if len(list) == 0 {
			continue
		}
		if _, err := m.SaveDocument(ctx, d, list); err != nil {
			return docs, chunks, fmt.Errorf("%s: %w", name, err)
		}
		docs++
		chunks += len(list)
	}
	return docs, chunks, nil
}

// categoryOf 按文件名粗分类: 业务文档归"流程", 其余归"技术".
func categoryOf(name string) string {
	for _, k := range []string{"流程", "策略", "方案", "复盘", "清单"} {
		if strings.Contains(name, k) {
			return "流程"
		}
	}
	return "技术"
}

type workOrder struct {
	ID          uint64 `gorm:"column:id"`
	OrderNo     string `gorm:"column:order_no"`
	Title       string `gorm:"column:title"`
	Type        int    `gorm:"column:type"`
	Status      int    `gorm:"column:status"`
	Description string `gorm:"column:description"`
}

func ingestWorkOrders(ctx context.Context, m *model.KBModel, tenant int64) (int, int, error) {
	dsn := os.Getenv("WORKORDER_MYSQL_DSN")
	if dsn == "" {
		fmt.Println("未设置 WORKORDER_MYSQL_DSN, 跳过工单经验")
		return 0, 0, nil
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return 0, 0, err
	}
	var rows []workOrder
	if err := db.WithContext(ctx).Raw(
		"SELECT id, order_no, title, type, status, COALESCE(description,'') AS description FROM work_order ORDER BY id LIMIT 500").
		Scan(&rows).Error; err != nil {
		return 0, 0, err
	}

	docs, chunks := 0, 0
	for _, r := range rows {
		var sb strings.Builder
		fmt.Fprintf(&sb, "工单编号：%s\n工单标题：%s\n工单类型：%s\n当前状态：%s\n\n问题描述：\n%s\n\n处置经验：\n"+
			"该工单来自园区历史记录，同类问题的处置可参考本单的流转过程（创建→指派→处理→完成）与处理时长分布。",
			r.OrderNo, r.Title, woType(r.Type), woStatus(r.Status), r.Description)
		content := sb.String()
		name := fmt.Sprintf("工单经验 #%d %s", r.ID, r.Title)
		d := &model.KbDocument{
			TenantID: uint64(tenant),
			Title:    truncateRunes(name, 200),
			Source:   fmt.Sprintf("workorder_db#%d", r.ID),
			Category: "工单经验",
			Content:  content,
			Status:   1,
		}
		pieces := chunker.Split(content, chunker.Options{MaxLen: 300, MinLen: 20})
		if len(pieces) == 0 {
			continue
		}
		list := make([]model.KbChunk, 0, len(pieces))
		for _, p := range pieces {
			list = append(list, model.KbChunk{Seq: p.Seq, Heading: p.Heading, Content: p.Content, CharLen: len([]rune(p.Content))})
		}
		if _, err := m.SaveDocument(ctx, d, list); err != nil {
			return docs, chunks, err
		}
		docs++
		chunks += len(list)
	}
	return docs, chunks, nil
}

func woType(t int) string {
	switch t {
	case 1:
		return "报修"
	case 2:
		return "巡检"
	case 3:
		return "投诉"
	case 4:
		return "保洁"
	default:
		return "其它"
	}
}

func woStatus(s int) string {
	switch s {
	case 1:
		return "待处理"
	case 2:
		return "处理中"
	case 3:
		return "已完成"
	case 4:
		return "已关闭"
	default:
		return "未知"
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
