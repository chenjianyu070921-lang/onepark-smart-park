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
		// FindTimeoutList 查询已超时但仍在等待(0待发送/1已下发)的指令, 供超时扫描任务处理
		FindTimeoutList(ctx context.Context, limit int) ([]*CommandLog, error)
		// MarkStatus 仅推进状态, 不改动 response
		MarkStatus(ctx context.Context, requestID string, status int8) error
		// MarkSent 推进状态并写入下发时间
		MarkSent(ctx context.Context, requestID string, status int8) error
		// ExtendTimeout 延长超时时间, 用于重发后重新计时
		ExtendTimeout(ctx context.Context, requestID string, timeoutAt time.Time) error
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
	var list []*CommandLog
	if err := m.db.WithContext(ctx).
		Where("status IN ? AND timeout_at < NOW()", []int8{CommandStatusPending, CommandStatusSent}).
		Limit(limit).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (m *commandLogModel) MarkStatus(ctx context.Context, requestID string, status int8) error {
	return m.db.WithContext(ctx).Model(&CommandLog{}).
		Where("request_id = ?", requestID).
		Update("status", status).Error
}
