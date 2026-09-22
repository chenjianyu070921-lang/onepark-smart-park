package model

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 集成用例: 依赖真实 MySQL, 未设置 ACCESS_TEST_DSN 时全部 Skip, 不影响常规 go test.
//
//	ACCESS_TEST_DSN='root:pwd@tcp(127.0.0.1:3306)/access_db?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true'
//
// 建表脚本由同包的 applyDDL(见 accessoperatelog_test.go)执行.
func realAccessDB(t *testing.T) *gorm.DB {
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

// TestIntegration_GrantIdempotentAndRevoke 验证 #45 幂等授权与 #46 撤销在真实 MySQL 上的行为.
func TestIntegration_GrantIdempotentAndRevoke(t *testing.T) {
	db := realAccessDB(t)
	permissions := NewPermissionModel(db)
	ctx := context.Background()
	const tenantID = 41

	window := `{"start":"08:00","end":"20:00","days":[1,2,3,4,5]}`
	newPermission := func(personID int64, deviceID string) *AccessPermission {
		now := time.Now()
		p := &AccessPermission{
			PersonID:   personID,
			DeviceID:   deviceID,
			TimeWindow: &window,
			Whitelist:  0,
			Status:     PermissionStatusValid,
		}
		p.TenantID = tenantID
		p.CreatedAt = now
		p.UpdatedAt = now
		return p
	}

	first := newPermission(901, "door-01")
	if _, err := permissions.Grant(ctx, []*AccessPermission{first}); err != nil {
		t.Fatalf("首次授权应成功: %v", err)
	}
	// 同一 (tenant, person, device) 重复授权: uk_person_device 冲突后转为更新, 不能报错也不能新增行.
	again := newPermission(901, "door-01")
	if _, err := permissions.Grant(ctx, []*AccessPermission{again}); err != nil {
		t.Fatalf("重复授权应被 upsert 吸收而非报错: %v", err)
	}

	var cnt int64
	if err := db.WithContext(ctx).Model(&AccessPermission{}).
		Where("tenant_id = ? AND person_id = ? AND device_id = ?", tenantID, 901, "door-01").
		Count(&cnt).Error; err != nil {
		t.Fatalf("count permissions: %v", err)
	}
	if cnt != 1 {
		t.Errorf("重复授权应保持唯一行, 实际 %d 行", cnt)
	}

	// 换设备 = 新权限行.
	if _, err := permissions.Grant(ctx, []*AccessPermission{newPermission(901, "door-02")}); err != nil {
		t.Fatalf("授权 door-02 失败: %v", err)
	}

	// 撤销单个设备后仅剩一条.
	revoked, err := permissions.Revoke(ctx, tenantID, []int64{901}, []string{"door-02"})
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if revoked != 1 {
		t.Errorf("期望撤销 1 条, 实际 %d", revoked)
	}

	// 撤销不存在的记录 RowsAffected 为 0, 由 logic 层转译错误.
	if n, err := permissions.Revoke(ctx, tenantID, []int64{999}, []string{"door-xx"}); err != nil || n != 0 {
		t.Errorf("撤销无匹配应返回 0: n=%d err=%v", n, err)
	}

	// 跨园区隔离: 另一个 tenant 的同名 person 不应被上面的撤销影响.
	other := newPermission(901, "door-01")
	other.TenantID = tenantID + 1
	if _, err := permissions.Grant(ctx, []*AccessPermission{other}); err != nil {
		t.Fatalf("写入其它园区权限失败: %v", err)
	}
	var remain int64
	if err := db.WithContext(ctx).Model(&AccessPermission{}).
		Where("tenant_id = ? AND person_id = ?", tenantID, 901).Count(&remain).Error; err != nil {
		t.Fatalf("count remain: %v", err)
	}
	if remain != 1 {
		t.Errorf("本园区应剩 1 条权限, 实际 %d", remain)
	}
}

// TestIntegration_RecordList 验证 #48 通行记录的分页、筛选与排序在真实库生效.
func TestIntegration_RecordList(t *testing.T) {
	db := realAccessDB(t)
	records := NewRecordModel(db)
	ctx := context.Background()
	const tenantID = 42

	// 建表脚本是 CREATE TABLE IF NOT EXISTS(不含 DROP), 而本用例会写入数据:
	// 不清理的话第二次运行会累加, "租户隔离"断言(total=3)必然失败 —— 用例必须可重复执行.
	if err := db.WithContext(ctx).Where("tenant_id IN ?", []int64{tenantID, tenantID + 1}).
		Delete(&AccessRecord{}).Error; err != nil {
		t.Fatalf("clean records: %v", err)
	}

	now := time.Now()
	seed := []*AccessRecord{
		{TenantID: tenantID, PersonID: 801, DeviceID: "door-01", Result: AccessResultSuccess, OpenType: OpenTypeCard, CreatedAt: now.Add(-3 * time.Hour)},
		{TenantID: tenantID, PersonID: 801, DeviceID: "door-02", Result: AccessResultFail, OpenType: OpenTypeFace, FailReason: "未授权", CreatedAt: now.Add(-2 * time.Hour)},
		{TenantID: tenantID, PersonID: 802, DeviceID: "door-01", Result: AccessResultSuccess, OpenType: OpenTypeRemote, CreatedAt: now.Add(-1 * time.Hour)},
		// 其它园区的数据不应出现在结果里.
		{TenantID: tenantID + 1, PersonID: 801, DeviceID: "door-01", Result: AccessResultSuccess, OpenType: OpenTypeQRCode, CreatedAt: now},
	}
	if err := db.WithContext(ctx).Create(&seed).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}

	list, total, err := records.List(ctx, AccessRecordFilter{TenantID: tenantID, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("租户隔离异常: total=%d len=%d", total, len(list))
	}
	// created_at DESC: 最新一条应为 remote 开门那条.
	if list[0].PersonID != 802 || list[0].OpenType != OpenTypeRemote {
		t.Errorf("排序异常, 首条为: %+v", list[0])
	}

	// 按结果筛选(失败记录).
	fail := AccessResultFail
	list, total, err = records.List(ctx, AccessRecordFilter{TenantID: tenantID, Result: &fail, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list failed records: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].FailReason != "未授权" {
		t.Errorf("按 result 筛选异常: total=%d list=%+v", total, list)
	}

	// 分页: 每页 2 条, 第 2 页应只剩 1 条.
	list, total, err = records.List(ctx, AccessRecordFilter{TenantID: tenantID, Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("paged list: %v", err)
	}
	if total != 3 || len(list) != 1 {
		t.Errorf("分页异常: total=%d page2 len=%d", total, len(list))
	}
}
