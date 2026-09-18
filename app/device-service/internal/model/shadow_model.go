package model

import (
	"context"

	"gorm.io/gorm"

	"onepark/common/shadow"
)

type (
	// ShadowModel 影子读写接口; 统一乐观锁语义:
	// Update* 按 version 条件更新并返回受影响行数, 0 行表示版本冲突(调用方决定重试或上抛).
	ShadowModel interface {
		Insert(ctx context.Context, s *shadow.Shadow) error
		FindByDeviceID(ctx context.Context, deviceID string) (*shadow.Shadow, error)
		// UpdateDesired 乐观锁更新期望值; 返回受影响行数, 0 表示版本冲突.
		UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) (int64, error)
		// UpdateReported 乐观锁更新上报值; 返回受影响行数, 0 表示版本冲突.
		UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) (int64, error)
		Delete(ctx context.Context, deviceID string) error
	}

	shadowModel struct {
		db *gorm.DB
	}
)

func NewShadowModel(db *gorm.DB) ShadowModel {
	return &shadowModel{db: db}
}

func (m *shadowModel) Insert(ctx context.Context, s *shadow.Shadow) error {
	return m.db.WithContext(ctx).Create(s).Error
}

func (m *shadowModel) FindByDeviceID(ctx context.Context, deviceID string) (*shadow.Shadow, error) {
	var s shadow.Shadow
	if err := m.db.WithContext(ctx).Where("device_id = ?", deviceID).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *shadowModel) UpdateDesired(ctx context.Context, deviceID string, desired []byte, version uint) (int64, error) {
	tx := m.db.WithContext(ctx).Model(&shadow.Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"desired": desired,
			"version": version + 1,
		})
	return tx.RowsAffected, tx.Error
}

func (m *shadowModel) UpdateReported(ctx context.Context, deviceID string, reported []byte, version uint) (int64, error) {
	tx := m.db.WithContext(ctx).Model(&shadow.Shadow{}).
		Where("device_id = ? AND version = ?", deviceID, version).
		Updates(map[string]any{
			"reported": reported,
			"version":  version + 1,
		})
	return tx.RowsAffected, tx.Error
}

func (m *shadowModel) Delete(ctx context.Context, deviceID string) error {
	return m.db.WithContext(ctx).Where("device_id = ?", deviceID).Delete(&shadow.Shadow{}).Error
}
