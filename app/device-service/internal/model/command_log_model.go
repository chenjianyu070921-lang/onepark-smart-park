package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type (
	CommandLogModel interface {
		Insert(ctx context.Context, c *CommandLog) error
		FindByRequestID(ctx context.Context, requestID string) (*CommandLog, error)
		UpdateStatus(ctx context.Context, requestID string, status int8, response []byte) error
		FindTimeoutList(ctx context.Context, limit int) ([]*CommandLog, error)
		MarkStatus(ctx context.Context, requestID string, status int8) error
		MarkSent(ctx context.Context, requestID string, status int8) error
		ExtendTimeout(ctx context.Context, requestID string, timeoutAt time.Time) error

		RecordSuccess(ctx context.Context, requestID string, response []byte, executedAt time.Time) error
		RecordFailureForRetry(ctx context.Context, requestID string, response []byte, executedAt time.Time, maxRetry int) error
		FindRetryList(ctx context.Context, limit int) ([]*CommandLog, error)
		ClaimRetry(ctx context.Context, requestID string, timeoutAt time.Time, maxRetry int) (bool, error)
		FinishTimeout(ctx context.Context, requestID string) error
	}

	commandLogModel struct {
		db *gorm.DB
	}
)

func NewCommandLogModel(db *gorm.DB) CommandLogModel {
	return &commandLogModel{db: db}
}

func (m *commandLogModel) Insert(ctx context.Context, c *CommandLog) error {
	return m.db.WithContext(ctx).Create(c).Error
}

func (m *commandLogModel) FindByRequestID(ctx context.Context, requestID string) (*CommandLog, error) {
	var c CommandLog
	if err := m.db.WithContext(ctx).Where("request_id = ?", requestID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *commandLogModel) UpdateStatus(ctx context.Context, requestID string, status int8, response []byte) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ?", requestID).
		Updates(map[string]any{
			"status":   status,
			"response": response,
		}).Error
}

func (m *commandLogModel) MarkSent(ctx context.Context, requestID string, status int8) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ?", requestID).
		Updates(map[string]any{
			"status":  status,
			"sent_at": gorm.Expr("NOW()"),
		}).Error
}

func (m *commandLogModel) ExtendTimeout(ctx context.Context, requestID string, timeoutAt time.Time) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ?", requestID).
		Updates(map[string]any{
			"timeout_at": timeoutAt,
			"status":     CommandStatusSent,
		}).Error
}

func (m *commandLogModel) FindTimeoutList(ctx context.Context, limit int) ([]*CommandLog, error) {
	return m.FindRetryList(ctx, limit)
}

func (m *commandLogModel) MarkStatus(ctx context.Context, requestID string, status int8) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ?", requestID).
		Update("status", status).Error
}

// RecordSuccess applies a successful acknowledgement only while the command can still
// transition from pending or sent. A zero-row update is an idempotent no-op.
func (m *commandLogModel) RecordSuccess(ctx context.Context, requestID string, response []byte, executedAt time.Time) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ? AND status IN ?", requestID, []int8{CommandStatusPending, CommandStatusSent}).
		Updates(map[string]any{
			"status":      CommandStatusSuccess,
			"response":    response,
			"executed_at": executedAt,
		}).Error
}

// RecordFailureForRetry keeps a failed command retryable while retry_count is below the
// configured limit. Once the limit is reached it writes terminal failed status.
func (m *commandLogModel) RecordFailureForRetry(ctx context.Context, requestID string, response []byte, executedAt time.Time, maxRetry int) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ? AND status IN ?", requestID, []int8{CommandStatusPending, CommandStatusSent}).
		Updates(map[string]any{
			"status":      gorm.Expr("CASE WHEN retry_count >= ? THEN ? ELSE ? END", maxRetry, CommandStatusFailed, CommandStatusSent),
			"response":    response,
			"executed_at": executedAt,
			"timeout_at":  executedAt,
		}).Error
}

func (m *commandLogModel) FindRetryList(ctx context.Context, limit int) ([]*CommandLog, error) {
	var list []*CommandLog
	if err := m.db.WithContext(ctx).
		Where("status IN ? AND timeout_at < NOW()", []int8{CommandStatusPending, CommandStatusSent}).
		Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ClaimRetry atomically reserves one resend. The status predicate prevents a successful
// acknowledgement from being overwritten, and retry_count < max prevents duplicate claims.
func (m *commandLogModel) ClaimRetry(ctx context.Context, requestID string, timeoutAt time.Time, maxRetry int) (bool, error) {
	result := m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ? AND status IN ? AND retry_count < ?", requestID,
			[]int8{CommandStatusPending, CommandStatusSent}, maxRetry).
		Updates(map[string]any{
			"retry_count": gorm.Expr("retry_count + 1"),
			"status":      CommandStatusSent,
			"timeout_at":  timeoutAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (m *commandLogModel) FinishTimeout(ctx context.Context, requestID string) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ? AND status IN ?", requestID, []int8{CommandStatusPending, CommandStatusSent}).
		Update("status", CommandStatusTimeout).Error
}
