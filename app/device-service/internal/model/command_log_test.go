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
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `status`=2,`response`=?,`executed_at`=? WHERE request_id = ? AND status IN (?,?)")).
		WithArgs([]byte(`{"ok":true}`), sqlmock.AnyArg(), "req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := m.RecordSuccess(context.Background(), "req-1", []byte(`{"ok":true}`), time.Now()); err != nil {
		t.Fatalf("RecordSuccess failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestRecordFailureForRetryKeepsSentBelowLimit(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `status`=CASE WHEN retry_count >= ? THEN 3 ELSE 1 END,`response`=?,`executed_at`=?,`timeout_at`=? WHERE request_id = ? AND status IN (?,?)")).
		WithArgs(2, []byte(`{"error":"failed"}`), sqlmock.AnyArg(), sqlmock.AnyArg(), "req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := m.RecordFailureForRetry(context.Background(), "req-1", []byte(`{"error":"failed"}`), time.Now(), 2); err != nil {
		t.Fatalf("RecordFailureForRetry failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestFindRetryListFiltersExpiredPendingOrSent(t *testing.T) {
	m, mock := newCommandLogTestDB(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `command_log` WHERE status IN (?,?) AND timeout_at < NOW() ORDER BY `command_log`.`id` LIMIT 200")).
		WithArgs(CommandStatusPending, CommandStatusSent).
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
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `retry_count`=retry_count+1,`status`=1,`timeout_at`=? WHERE request_id = ? AND status IN (?,?) AND retry_count < ?")).
		WithArgs(sqlmock.AnyArg(), "req-1", CommandStatusPending, CommandStatusSent, 2).
		WillReturnResult(sqlmock.NewResult(0, 1))

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
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `command_log` SET `status`=4 WHERE request_id = ? AND status IN (?,?)")).
		WithArgs("req-1", CommandStatusPending, CommandStatusSent).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := m.FinishTimeout(context.Background(), "req-1"); err != nil {
		t.Fatalf("FinishTimeout failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
