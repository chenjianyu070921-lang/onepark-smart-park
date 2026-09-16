package main

import (
	"fmt"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	dsn := "root:4ay1nka13u8ed77Y@tcp(115.191.16.159:3306)/onepark-smart-park?charset=utf8mb4&parseTime=true&loc=Local"

	fmt.Println("正在连接 MySQL 115.191.16.159:3306 ...")
	start := time.Now()

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Printf("连接失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("连接成功！耗时: %v\n", time.Since(start))

	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	var result struct {
		One   int
		Now   string
		DbName string
	}

	if err := db.Raw("SELECT 1 AS one, NOW() AS now, DATABASE() AS db_name").Scan(&result).Error; err != nil {
		fmt.Printf("查询失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("查询成功: 1=%d, NOW=%s, DB=%s\n", result.One, result.Now, result.DbName)

	var tableCount int64
	db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'onepark-smart-park'").Scan(&tableCount)
	fmt.Printf("数据库表数量: %d\n", tableCount)

	var tableNames []string
	db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = 'onepark-smart-park' ORDER BY table_name").Scan(&tableNames)
	for _, name := range tableNames {
		fmt.Printf("  - %s\n", name)
	}

	sqlDB.Close()
	fmt.Println("测试完成，连接已关闭。")
}
