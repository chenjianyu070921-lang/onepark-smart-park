package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 集成用例: 依赖真实 MySQL, 未设置 VIDEO_TEST_DSN 时全部 Skip, 不影响常规 go test.
//
//	VIDEO_TEST_DSN='root:pwd@tcp(127.0.0.1:3306)/video_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//
// 建表脚本含中文注释, SET NAMES 必须与建表语句同批执行(连接池换连接会失效).
func realVideoDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("VIDEO_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 VIDEO_TEST_DSN, 跳过 MySQL 集成用例")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}
	abs, err := filepath.Abs("../../../../deploy/sql/m3_video_mysql_tables.sql")
	if err != nil {
		t.Fatalf("resolve ddl: %v", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read ddl: %v", err)
	}
	if err := db.Exec("SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;\n" + string(raw)).Error; err != nil {
		t.Fatalf("apply ddl: %v", err)
	}
	return db
}

func newCamera(tenantID int64, deviceID string, status int8) *Camera {
	now := time.Now()
	location := `{"lng":113.12,"lat":23.03,"floor":"3F"}`
	c := &Camera{
		Name:      "1号厂房西北角",
		DeviceID:  deviceID,
		AreaID:    12,
		RtspURL:   "rtsp://192.168.1.50:554/stream1",
		Location:  &location,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	c.TenantID = tenantID
	return c
}

// TestIntegration_CameraCreateAndList 验证摄像头写入(含 status=0 离线)与列表筛选、租户隔离.
func TestIntegration_CameraCreateAndList(t *testing.T) {
	db := realVideoDB(t)
	cameras := NewCameraModel(db)
	ctx := context.Background()
	const tenantID = 66
	// 本用例独占两个租户(tenantID 与 tenantID+1, 后者用于跨园区同设备ID场景),
	// 两个都要清理: 只清 tenantID 会让上一次运行留下的 tenantID+1 记录令重复插入断言失败(用例不可重复执行).
	if err := db.WithContext(ctx).
		Where("tenant_id IN ?", []int64{tenantID, tenantID + 1}).
		Delete(&Camera{}).Error; err != nil {
		t.Fatalf("clean cameras: %v", err)
	}

	// status=0(离线)必须能真实落库 —— 曾因 GORM default 标签被跳过(见 alarm/access 同类 Bug).
	offline := newCamera(tenantID, "cam-offline", CameraStatusOffline)
	if err := cameras.Create(ctx, offline); err != nil {
		t.Fatalf("写入离线摄像头失败: %v", err)
	}
	online := newCamera(tenantID, "cam-online", CameraStatusOnline)
	if err := cameras.Create(ctx, online); err != nil {
		t.Fatalf("写入在线摄像头失败: %v", err)
	}

	got, err := cameras.FindByID(ctx, tenantID, offline.ID)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got.Status != CameraStatusOffline {
		t.Errorf("status=0 未真实落库(零值被吞): %d", got.Status)
	}
	if got.Location == nil {
		t.Error("位置 JSON 未回读")
	}

	// 同一租户下重复 device_id 必须报重复错误(uk_device).
	dup := newCamera(tenantID, "cam-online", CameraStatusOnline)
	if err := cameras.Create(ctx, dup); err != ErrCameraDuplicate {
		t.Errorf("重复设备ID应返回 ErrCameraDuplicate, 实际 %v", err)
	}

	// 不同租户可用同一 device_id(唯一索引含 tenant_id).
	other := newCamera(tenantID+1, "cam-online", CameraStatusOnline)
	if err := cameras.Create(ctx, other); err != nil {
		t.Errorf("跨租户同设备ID应允许: %v", err)
	}

	// 按状态筛选: 只统计本租户.
	onlineStatus := CameraStatusOnline
	list, total, err := cameras.List(ctx, CameraListFilter{TenantID: tenantID, Status: &onlineStatus, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].DeviceID != "cam-online" {
		t.Errorf("按状态筛选异常: total=%d list=%+v", total, list)
	}

	// 不存在的摄像头与跨园区隔离.
	if _, err := cameras.FindByID(ctx, tenantID, 999999); err != ErrCameraNotFound {
		t.Errorf("不存在应返回 ErrCameraNotFound, 实际 %v", err)
	}
	if _, err := cameras.FindByID(ctx, tenantID+99, online.ID); err != ErrCameraNotFound {
		t.Errorf("跨园区不应可见, 实际 %v", err)
	}
}
