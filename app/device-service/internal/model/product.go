package model

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Product struct {
	ID          uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductKey  string         `gorm:"column:product_key;type:varchar(32);uniqueIndex:uk_product_key;not null" json:"product_key"`
	ProductName string         `gorm:"column:product_name;type:varchar(64);not null;default:''" json:"product_name"`
	Description string         `gorm:"column:description;type:varchar(255);not null;default:''" json:"description"`
	ThingModel  datatypes.JSON `gorm:"column:thing_model;type:json" json:"thing_model"`
	Status      int8           `gorm:"column:status;type:tinyint;not null;default:1" json:"status"`
	CreatedAt   time.Time      `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"column:deleted_at;index" json:"deleted_at"`
}

func (Product) TableName() string {
	return "product"
}

func (p *Product) GetThingModel() (map[string]any, error) {
	if p.ThingModel == nil {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(p.ThingModel, &m); err != nil {
		return nil, err
	}
	return m, nil
}
