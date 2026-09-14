package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	ShadowModel interface {
		Insert(ctx context.Context, s *Shadow) error
		FindByDeviceID(ctx context.Context, deviceID string) (*Shadow, error)
		UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) error
		UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) error
		Delete(ctx context.Context, deviceID string) error
	}

	shadowModel struct {
		db *gorm.DB
	}
)

func NewShadowModel(db *gorm.DB) ShadowModel {
	return &shadowModel{db: db}
}

func (m *shadowModel) Insert(ctx context.Context, s *Shadow) error {
	return m.db.WithContext(ctx).Create(s).Error
}

func (m *shadowModel) FindByDeviceID(ctx context.Context, deviceID string) (*Shadow, error) {
	var s Shadow
	if err := m.db.WithContext(ctx).Where("device_id = ?", deviceID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *shadowModel) UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) error {
	return m.db.WithContext(ctx).Model(&Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"desired": desired,
			"version": version + 1,
		}).Error
}

func (m *shadowModel) UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) error {
	return m.db.WithContext(ctx).Model(&Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"reported": reported,
			"version":  version + 1,
		}).Error
}

func (m *shadowModel) Delete(ctx context.Context, deviceID string) error {
	return m.db.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&Shadow{}).Error
}
