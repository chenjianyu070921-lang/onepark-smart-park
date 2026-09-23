// dbexplain 列表/扫描查询的执行计划验证（计划书 Day 4 加练 C）。
//
// 为什么必须"造几千条再看": 几十行的表上优化器**必然**走全表扫描 —— 反正是全表，
// 走索引反而更贵。所以空表/小表上的 EXPLAIN 结论是**没有意义**的，必须先把数据量顶上去。
//
// 为什么用**影子表**而不是往真表灌数据:
// 见 dbexplain 的取舍 —— 本项目库里是答辩/演示用的真实数据，往 dispatch_task 灌 5000 条
// 会改掉工单列表的展示内容；而 EXPLAIN 关心的只是**数据分布**，影子表用 CREATE TABLE ... LIKE
// 能完整复制列与索引，量出来的执行计划与真表等价。用完即 DROP，零残留。
//
// 用法:
//
//	go run ./app/dispatch-service/tools/dbexplain -dsn "root:xxx@tcp(127.0.0.1:3306)/" -rows 5000
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// explainCase 一条待验证的查询。
// SQL 里的 {t} 是表名占位符(影子表名), 其余部分**逐字来自业务代码**, 见 source 引用。
type explainCase struct {
	name   string
	table  string // 真实表(用于 CREATE TABLE ... LIKE 与库名)
	sql    string // 含 {t} 占位符
	source string // 出处: 文件:行
	why    string // 这条为什么值得验证
}

var cases = []explainCase{
	{
		name: "工单列表·状态+优先级", table: "dispatch_db.dispatch_task",
		sql:    `SELECT * FROM {t} WHERE status = 2 AND priority = 1 ORDER BY id DESC LIMIT 20`,
		source: "internal/logic/*/tasklistlogic.go:52,55", why: "索引 idx_status_priority(status,priority) 是否真被选中",
	},
	{
		name: "工单列表·仅状态", table: "dispatch_db.dispatch_task",
		sql:    `SELECT * FROM {t} WHERE status = 2 ORDER BY id DESC LIMIT 20`,
		source: "internal/logic/*/tasklistlogic.go:52", why: "只按状态时能否用上 idx_status_priority 的前缀",
	},
	{
		name: "工单列表·园区+状态", table: "dispatch_db.dispatch_task",
		// 取值与本工具生成的分布一致(zone_code = Z-<n%4>-101); 查不到行会让计划失真
		sql:    `SELECT * FROM {t} WHERE zone_code = 'Z-2-101' AND status = 2 ORDER BY id DESC LIMIT 20`,
		source: "索引 idx_zone_status 的设计目标", why: "组合索引顺序 (zone_code,status) 是否匹配查询",
	},
	{
		name: "重派扫描·到期工单", table: "dispatch_db.dispatch_task",
		sql:    `SELECT * FROM {t} WHERE status = 2 AND assign_expire_at IS NOT NULL AND assign_expire_at < NOW() LIMIT 100`,
		source: "internal/cron/reassign.go:106", why: "⭐ 定时任务每轮都跑; assign_expire_at 无索引, 全表扫描随工单量增长",
	},
	{
		name: "乐观锁条件", table: "dispatch_db.dispatch_task",
		// 真实代码是 `id = ? AND version = ? AND status = ?`(reassign.go:174,204)。
		// 这里省掉 version: 影子表的 version 取自样例行, 写死具体值会与样本不符,
		// 优化器会直接判定 Impossible WHERE 而给不出计划(踩过)。少一个等值过滤不影响
		// 「是否走主键」这个结论。status=2 与本工具生成的分布一致(n%5+1, id=1 -> 2)。
		sql:    `SELECT * FROM {t} WHERE id = 1 AND status = 2`,
		source: "internal/cron/reassign.go:174,204", why: "主键等值, 应是 const/eq_ref 级",
	},
	{
		name: "合同列表·状态", table: "leasing_db.lease_contract",
		sql:    `SELECT * FROM {t} WHERE status = 1 ORDER BY id DESC LIMIT 20`,
		source: "internal/logic/*/contractlistlogic.go:55", why: "idx_status_end_date 的前缀可用性",
	},
	{
		name: "即将到期合同", table: "leasing_db.lease_contract",
		sql:    `SELECT * FROM {t} WHERE status = 1 AND tenant_id = 1 ORDER BY end_date ASC LIMIT 20`,
		source: "internal/logic/*/contractexpiringlogic.go:66", why: "tenant_id 与 (status,end_date) 索引的竞争",
	},
	{
		name: "每日到期扫描", table: "leasing_db.lease_contract",
		sql:    `SELECT * FROM {t} WHERE status = 1 AND auto_renew = 0 AND end_date < NOW() LIMIT 100`,
		source: "internal/cron/daily.go:96,147", why: "⭐ auto_renew 卡在中间, end_date 的范围条件能否用上",
	},
	{
		name: "账单列表·租户+状态", table: "leasing_db.lease_bill",
		sql:    `SELECT * FROM {t} WHERE tenant_id = 1 AND status = 1 ORDER BY id DESC LIMIT 20`,
		source: "internal/logic/*/billlistlogic.go:62,67", why: "⭐ lease_bill 上没有 tenant_id 索引",
	},
	{
		name: "账单列表·周期+状态", table: "leasing_db.lease_bill",
		sql:    `SELECT * FROM {t} WHERE billing_period = '2026-09' AND status = 1 ORDER BY id DESC LIMIT 20`,
		source: "internal/logic/*/billlistlogic.go:59", why: "idx_period_status 的前缀可用性",
	},
}

type column struct {
	name  string
	key   string // PRI / UNI / MUL —— ⚠️ 复合唯一键的非首列这里只是 MUL, 不能靠它判断
	typ   string
	null  string
	extra string
	// uniqFirst = 本列是某个唯一索引(含复合)的第一列。
	// 由 information_schema.statistics 判定 —— COLUMN_KEY 对复合唯一键首列**会漏报**,
	// 漏报的后果是灌数时撞唯一键(Duplicate entry ... uk_contract_period)。
	uniqFirst bool
}

func main() {
	dsn := flag.String("dsn", "", "MySQL DSN(不含库名, 因为是跨库查询)")
	rows := flag.Int("rows", 5000, "影子表目标行数")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "[dbexplain] 必须提供 -dsn")
		os.Exit(1)
	}

	db, err := gorm.Open(mysql.Open(*dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dbexplain] 连接失败: %v\n", err)
		os.Exit(1)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(4)

	// 每张真实表建一个影子表(只建一次), 灌到目标行数
	shadows := map[string]string{}
	for _, c := range cases {
		if _, ok := shadows[c.table]; ok {
			continue
		}
		num := int(time.Now().UnixNano() % 1000000)
		shadow := fmt.Sprintf("%s__x%d", c.table, num) // 保留库名前缀: dispatch_db.dispatch_task__x123
		if err := buildShadow(db, c.table, shadow, *rows); err != nil {
			fmt.Fprintf(os.Stderr, "[dbexplain] 影子表 %s 准备失败: %v\n", shadow, err)
			os.Exit(1)
		}
		shadows[c.table] = shadow
		defer func(s string) { db.Exec("DROP TABLE IF EXISTS " + s) }(shadow)
	}

	fmt.Printf("\n=== 执行计划验证(影子表复制真实表结构+索引, 数据量 %d 行) ===\n\n", *rows)
	bad := 0
	for _, c := range cases {
		shadow := shadows[c.table]
		q := strings.ReplaceAll(c.sql, "{t}", shadow)
		plan, err := explain(db, q)
		if err != nil {
			fmt.Printf("【%s】EXPLAIN 失败: %v\n\n", c.name, err)
			bad++
			continue
		}
		verdict := "✅ 走索引"
		if plan.typ == "ALL" {
			verdict = "⚠️ 全表扫描(TYPE=ALL)"
			bad++
		}
		if plan.typ == "" {
			verdict = "⚠️ 计划为空"
		}
		fmt.Printf("【%s】%s\n", c.name, verdict)
		fmt.Printf("  出处 %s\n", c.source)
		fmt.Printf("  关心 %s\n", c.why)
		fmt.Printf("  SQL  %s\n", c.sql)
		fmt.Printf("  计划 type=%s key=%s rows=%d filtered=%s Extra=%s\n\n",
			plan.typ, nz(plan.key), plan.rows, plan.filtered, nz(plan.extra))
	}

	if bad > 0 {
		fmt.Printf("=== 结论: %d/%d 条查询未走索引(见上面 ⚠️) ===\n", bad, len(cases))
	} else {
		fmt.Printf("=== 结论: %d 条查询全部走索引 ✅ ===\n", len(cases))
	}
}

type planRow struct {
	typ      string
	key      string
	rows     int64
	filtered string
	extra    string
}

func explain(db *gorm.DB, q string) (planRow, error) {
	var out []map[string]any
	if err := db.Raw("EXPLAIN " + q).Scan(&out).Error; err != nil {
		return planRow{}, err
	}
	if len(out) == 0 {
		return planRow{}, nil
	}
	r := out[0]
	return planRow{
		typ:      str(r["type"]),
		key:      str(r["key"]),
		rows:     i64(r["rows"]),
		filtered: str(r["filtered"]),
		extra:    str(r["Extra"]),
	}, nil
}

// buildShadow 建影子表并灌到目标行数。
// 灌数方式: 先从容表拷 1 行(拿到真实取值分布), 然后**反复自我复制翻倍** ——
// 5000 行只需要约 13 次 INSERT, 比逐行插快两个数量级。
func buildShadow(db *gorm.DB, real, shadow string, rows int) error {
	if err := db.Exec("DROP TABLE IF EXISTS " + shadow).Error; err != nil {
		return err
	}
	if err := db.Exec(fmt.Sprintf("CREATE TABLE %s LIKE %s", shadow, real)).Error; err != nil {
		return err
	}

	cols, err := tableColumns(db, shadow)
	if err != nil {
		return err
	}
	// 自增主键交给 MySQL 自己生成, 其余列由 Go 按行号算好再灌。
	//
	// 为什么不走 `INSERT ... SELECT` 自我复制翻倍(那样只要十几次语句):
	// 每一轮都要重新生成唯一列的值, 而 **RAND() 在 INSERT...SELECT 里不保证逐行求值** ——
	// 实测直接撞唯一键: `Duplicate entry ... for key uk_task_no`。
	// 行号由 Go 给出才是确定性的, 且顺带能把"分布"做出来(见 valueFor)。
	if err := markUniqueFirst(db, shadow, cols); err != nil {
		return err
	}
	cols = withoutAutoPK(cols)
	if len(cols) == 0 {
		return fmt.Errorf("表 %s 没有可写入的列", real)
	}
	sample, err := sampleRow(db, real, cols)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, "`"+c.name+"`")
	}
	colList := strings.Join(names, ",")

	const batch = 200
	now := time.Now()
	for start := 0; start < rows; start += batch {
		size := batch
		if start+size > rows {
			size = rows - start
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO " + shadow + " (" + colList + ") VALUES ")
		args := make([]any, 0, size*len(cols))
		for i := 0; i < size; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(")
			n := start + i + 1
			for j, c := range cols {
				if j > 0 {
					sb.WriteString(",")
				}
				sb.WriteString("?")
				args = append(args, valueFor(c, sample[c.name], n, now))
			}
			sb.WriteString(")")
		}
		if err := db.Exec(sb.String(), args...).Error; err != nil {
			return err
		}
	}

	// 让优化器看到统计信息 —— 不 ANALYZE 的话计划可能还是按"没数据"来估
	if err := db.Exec("ANALYZE TABLE " + shadow).Error; err != nil {
		return err
	}
	fmt.Printf("[dbexplain] 影子表 %s 就绪(%d 行, 列 %d 个)\n", shadow, rows, len(cols))
	return nil
}

func withoutAutoPK(cols []column) []column {
	out := make([]column, 0, len(cols))
	for _, c := range cols {
		if c.key == "PRI" && strings.Contains(strings.ToLower(c.extra), "auto_increment") {
			continue
		}
		out = append(out, c)
	}
	return out
}

// sampleRow 取真表一行作为取值样本(全部转成字符串, 交给 MySQL 自行转换类型)。
func sampleRow(db *gorm.DB, real string, cols []column) (map[string]any, error) {
	var cnt int64
	if err := db.Raw("SELECT COUNT(*) FROM " + real).Scan(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt == 0 {
		return nil, fmt.Errorf("真表 %s 为空, 取不到样例行", real)
	}
	// 只取需要的列: SELECT * 会把被剔掉的自增主键也带回来, 与 Scan 的目标数对不上
	sel := make([]string, 0, len(cols))
	for _, c := range cols {
		sel = append(sel, "`"+c.name+"`")
	}
	rows, err := db.Raw("SELECT " + strings.Join(sel, ",") + " FROM " + real + " LIMIT 1").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if !rows.Next() {
		return nil, fmt.Errorf("真表 %s 取不到样例行", real)
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(cols))
	for i, c := range cols {
		out[c.name] = normalize(raw[i])
	}
	return out, nil
}

// valueFor 给第 n 行决定各列的取值。
//
// 关键在"分布": 全表同一组值的影子表会让优化器选出**失真**的计划
// (比如 status 全一样时, 走索引反而更贵, 计划自然就变成全表扫描)。
// 所以对查询真正用得上的那几列按行号做低基数分布 —— 与真实数据的形态一致。
func valueFor(c column, sample any, n int, now time.Time) any {
	switch c.name {
	case "status":
		return n%5 + 1 // 5 种状态轮转
	case "priority":
		return n%3 + 1
	case "tenant_id":
		return n%3 + 1
	case "auto_renew":
		return n % 2
	case "zone_code":
		return fmt.Sprintf("Z-%d-101", n%4)
	case "billing_period":
		return fmt.Sprintf("2026-%02d", n%12+1)
	case "assign_expire_at", "end_date", "start_date", "expire_at", "create_time", "update_time":
		// 尽量都落在过去: 到期扫描/重派扫描这类**范围查询**需要真的有匹配行, 否则计划会走成空集
		return now.Add(-time.Duration(n%720) * time.Hour).Format("2006-01-02 15:04:05")
	}
	if c.uniqFirst {
		return uniqueValue(c, sample, n)
	}
	return sample
}

// markUniqueFirst 标出「每个唯一索引(含复合)的第一列」—— 只打散首列即可保证整键唯一。
func markUniqueFirst(db *gorm.DB, table string, cols []column) error {
	rows, err := db.Raw(`SELECT DISTINCT COLUMN_NAME FROM information_schema.statistics
		WHERE CONCAT(TABLE_SCHEMA,'.',TABLE_NAME) = ? AND NON_UNIQUE = 0 AND SEQ_IN_INDEX = 1
		  AND INDEX_NAME <> 'PRIMARY'`, table).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	first := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		first[name] = true
	}
	for i := range cols {
		if first[cols[i].name] {
			cols[i].uniqFirst = true
		}
	}
	return rows.Err()
}

// uniqueValue 唯一列: 用行号打散。
//
// 两个坑都踩过:
//   - 字符串列: 必须**按列长度截断**, 否则 MySQL 报 `Data truncated for column ...`
//   - 数值列: **不能拼字符串** —— `'1-000001'` 会被隐式转成 1(截断), 唯一性当场失效,
//     表现为撞 `uk_contract_period` 这类复合唯一键。数值列要用数值法加行号。
func uniqueValue(c column, sample any, n int) any {
	if isNumericType(c.typ) {
		var base int64
		fmt.Sscanf(toString(sample), "%d", &base)
		return base*1_000_000 + int64(n)
	}
	s := toString(sample)
	if limit := varcharLen(c.typ); limit > 0 {
		keep := limit - 7
		if keep < 1 {
			keep = 1
		}
		if len(s) > keep {
			s = s[:keep]
		}
	}
	return fmt.Sprintf("%s-%06d", s, n%1000000)
}

// normalize 把驱动返回的值收成可写回 MySQL 的形式。
// ⚠️ time.Time 不能直接 %v: 会变成 `2026-09-15 16:01:07 +0800 CST`, MySQL 拒收
// (`Incorrect datetime value`, 踩过一次)。nil 必须保持 nil(否则会被当成空串写进 datetime 列)。
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	default:
		return v
	}
}

func toString(v any) string {
	n := normalize(v)
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%v", n)
}

// isNumericType ⚠️ 用 Contains 而不是 HasPrefix: `bigint` / `smallint` / `tinyint` 都不以
// `int` 开头(踩过 —— 漏判导致数值列仍走字符串路径, MySQL 报 Data truncated)。
func isNumericType(typ string) bool {
	t := strings.ToLower(typ)
	for _, p := range []string{"int", "decimal", "numeric", "double", "float", "bit"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// varcharLen 从 `varchar(64)` / `char(32)` 里取长度; 非字符类型返回 0。
func varcharLen(typ string) int {
	t := strings.ToLower(typ)
	if !strings.HasPrefix(t, "varchar") && !strings.HasPrefix(t, "char") {
		return 0
	}
	i, j := strings.IndexByte(t, '('), strings.IndexByte(t, ')')
	if i < 0 || j <= i {
		return 0
	}
	n := 0
	fmt.Sscanf(t[i+1:j], "%d", &n)
	return n
}

func tableColumns(db *gorm.DB, table string) ([]column, error) {
	// 刻意用 Rows() + 显式 Scan 而不是 GORM 的 Scan(&struct): 后者靠列名映射字段名,
	// 遇到 `key` / `null` 这类保留字别名会**静默扫成空值**(实测: 列名全空 -> SQL 拼成 ",,,," 报 1064)。
	rows, err := db.Raw(`SELECT COLUMN_NAME, COLUMN_KEY, COLUMN_TYPE, IS_NULLABLE, EXTRA
		FROM information_schema.columns WHERE CONCAT(TABLE_SCHEMA,'.',TABLE_NAME) = ?
		ORDER BY ORDINAL_POSITION`, table).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.name, &c.key, &c.typ, &c.null, &c.extra); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

func i64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case []byte:
		var x int64
		fmt.Sscanf(string(t), "%d", &x)
		return x
	default:
		return 0
	}
}

func nz(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
