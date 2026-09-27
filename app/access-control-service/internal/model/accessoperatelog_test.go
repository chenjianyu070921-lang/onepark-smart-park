package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 集成用例: 依赖真实 MySQL, 未设置 ACCESS_TEST_DSN 时全部 Skip, 不影响常规 go test.
//
//	ACCESS_TEST_DSN='root:pwd@tcp(127.0.0.1:3306)/access_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//
// 注意: 初始化会执行建表脚本(含中文注释), 请指向专用的本地测试库.
// ddlPath 相对本文件所在目录(internal/model), 需要上溯四级才到仓库根.
const ddlPath = "../../../../deploy/sql/m3_access_mysql_tables.sql"

func realDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 ACCESS_TEST_DSN, 跳过 MySQL 集成用例")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	applyDDL(t, db)
	return db
}

// applyDDL 执行建表脚本, 同时验证 deploy/sql/m3_access_mysql_tables.sql 在真实 MySQL 上可跑通.
func applyDDL(t *testing.T, db *gorm.DB) {
	t.Helper()
	abs, err := filepath.Abs(ddlPath)
	if err != nil {
		t.Fatalf("resolve ddl path: %v", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read ddl %s: %v", abs, err)
	}
	// 建表脚本含中文注释, SET NAMES 必须与建表语句同批执行(连接池换连接会失效).
	script := "SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;\n" + string(raw)
	if err := db.Exec(script).Error; err != nil {
		t.Fatalf("apply ddl %s: %v", abs, err)
	}
}

// TestIntegration_OperateLogCreate 验证远程开门审计在真实库上按成功/失败/超时三种结果落库.
func TestIntegration_OperateLogCreate(t *testing.T) {
	db := realDB(t)
	logs := NewOperateLogModel(db)
	ctx := context.Background()

	cases := map[string]int8{
		"success": CommandResultSuccess,
		"fail":    CommandResultFail,
		"timeout": CommandResultTimeout,
	}
	for name, result := range cases {
		now := time.Now()
		entry := &AccessOperateLog{
			DeviceID:   "door-01",
			OperatorID: 9,
			Command:    "open_door",
			Result:     result,
			Message:    fmt.Sprintf("集成测试-%s", name),
			Reason:     "访客放行",
		}
		entry.TenantID = 1
		entry.CreatedAt = now
		entry.UpdatedAt = now
		if err := logs.Create(ctx, entry); err != nil {
			t.Fatalf("写入 %s 审计失败: %v", name, err)
		}
		if entry.ID == 0 {
			t.Errorf("%s 写入后应回填自增主键", name)
		}
	}

	var rows []AccessOperateLog
	if err := db.WithContext(ctx).Where("tenant_id = ? AND device_id = ?", 1, "door-01").
		Order("id DESC").Limit(3).Find(&rows).Error; err != nil {
		t.Fatalf("reload logs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("期望 3 条审计, 实际 %d 条", len(rows))
	}
	for _, r := range rows {
		if r.DeviceID != "door-01" || r.OperatorID != 9 || r.Command != "open_door" || r.Reason != "访客放行" {
			t.Errorf("回读内容与写入不一致: %+v", r)
		}
		if r.Result != CommandResultSuccess && r.Result != CommandResultFail && r.Result != CommandResultTimeout {
			t.Errorf("result 取值越界: %d", r.Result)
		}
	}
}
