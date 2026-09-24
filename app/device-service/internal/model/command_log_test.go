package model

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newCommandLogTestDB(t *testing.T) (*commandLogModel, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock failed: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	return &commandLogModel{db: db}, mock
}

func TestRecordSuccessOnlyUpdatesPendingOrSent(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	// GORM 默认写操作包裹隐式事务(SkipDefaultTransaction=false), 必须期望 Begin/Commit
	mock.ExpectBegin()
	// GORM 对 Updates(map) 按列名字母序生成 SET 子句, 参数顺序随之调整
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `executed_at`=?,`response`=?,`status`=? WHERE request_id = ? AND status IN (?,?)")).
		WithArgs(sqlmock.AnyArg(), []byte(`{"ok":true}`), CommandStatusSuccess, "req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := m.RecordSuccess(context.Background(), "req-1", []byte(`{"ok":true}`), time.Now()); err != nil {
		t.Fatalf("RecordSuccess failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestRecordFailureForRetryKeepsSentBelowLimit(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	mock.ExpectBegin()
	// 列序字母化: executed_at/response/status(CASE 表达式参数内联)/timeout_at
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `executed_at`=?,`response`=?,`status`=CASE WHEN retry_count >= ? THEN ? ELSE ? END,`timeout_at`=? WHERE request_id = ? AND status IN (?,?)")).
		WithArgs(sqlmock.AnyArg(), []byte(`{"error":"failed"}`), 2, CommandStatusFailed, CommandStatusSent, sqlmock.AnyArg(), "req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := m.RecordFailureForRetry(context.Background(), "req-1", []byte(`{"error":"failed"}`), time.Now(), 2); err != nil {
		t.Fatalf("RecordFailureForRetry failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestFindRetryListFiltersExpiredPendingOrSent(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	// 断言口径对齐现行模型: FindRetryList 不带 ORDER BY, Limit 走占位符参数
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `command_log` WHERE status IN (?,?) AND timeout_at < NOW() LIMIT ?")).
		WithArgs(CommandStatusPending, CommandStatusSent, 200).
		WillReturnRows(sqlmock.NewRows([]string{"id", "request_id"}).AddRow(1, "req-1"))

	list, err := m.FindRetryList(context.Background(), 200)
	if err != nil {
		t.Fatalf("FindRetryList failed: %v", err)
	}
	if len(list) != 1 || list[0].RequestID != "req-1" {
		t.Fatalf("list = %+v, want req-1", list)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestClaimRetryIncrementsOnlyBelowLimit(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	mock.ExpectBegin()
	// gorm.Expr 原样输出 "retry_count + 1"(含空格), status/timeout_at 走占位符
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `retry_count`=retry_count + 1,`status`=?,`timeout_at`=? WHERE request_id = ? AND status IN (?,?) AND retry_count < ?")).
		WithArgs(CommandStatusSent, sqlmock.AnyArg(), "req-1", CommandStatusPending, CommandStatusSent, 2).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	claimed, err := m.ClaimRetry(context.Background(), "req-1", time.Now().Add(30*time.Second), 2)
	if err != nil {
		t.Fatalf("ClaimRetry failed: %v", err)
	}
	if !claimed {
		t.Fatal("expected claim to succeed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestFinishTimeoutOnlyUpdatesPendingOrSent(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `status`=? WHERE request_id = ? AND status IN (?,?)")).
		WithArgs(CommandStatusTimeout, "req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := m.FinishTimeout(context.Background(), "req-1"); err != nil {
		t.Fatalf("FinishTimeout failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
