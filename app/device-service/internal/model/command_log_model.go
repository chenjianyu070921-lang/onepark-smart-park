package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	CommandLogModel interface {
		Insert(ctx context.Context, c *CommandLog) error
		FindByRequestID(ctx context.Context, requestID string) (*CommandLog, error)
		UpdateStatus(ctx context.Context, requestID string, status int8, response []byte) error
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
