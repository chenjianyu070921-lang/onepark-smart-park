package model

import (
	"context"

	"gorm.io/gorm"
)

type (
	ProductModel interface {
		Insert(ctx context.Context, p *Product) error
		FindByKey(ctx context.Context, productKey string) (*Product, error)
		FindList(ctx context.Context, page, size int, status int8) ([]*Product, int64, error)
		Update(ctx context.Context, p *Product) error
	}

	productModel struct {
		db *gorm.DB
	}
)

func NewProductModel(db *gorm.DB) ProductModel {
	return &productModel{db: db}
}

func (m *productModel) Insert(ctx context.Context, p *Product) error {
	return m.db.WithContext(ctx).Create(p).Error
}

func (m *productModel) FindByKey(ctx context.Context, productKey string) (*Product, error) {
	var p Product
	if err := m.db.WithContext(ctx).Where("product_key = ? AND deleted_at IS NULL", productKey).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *productModel) FindList(ctx context.Context, page, size int, status int8) ([]*Product, int64, error) {
	var list []*Product
	var total int64
	tx := m.db.WithContext(ctx).Model(&Product{}).Where("deleted_at IS NULL")
	if status != -1 {
		tx = tx.Where("status = ?", status)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (m *productModel) Update(ctx context.Context, p *Product) error {
	return m.db.WithContext(ctx).Save(p).Error
}
