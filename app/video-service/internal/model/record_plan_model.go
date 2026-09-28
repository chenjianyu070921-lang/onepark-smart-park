package model

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type recordPlanModel struct {
	db *gorm.DB
}

// NewRecordPlanModel 构造基于 GORM 的录像计划数据访问实现.
func NewRecordPlanModel(db *gorm.DB) RecordPlanModel {
	return &recordPlanModel{db: db}
}

// Create 写入录像计划. uk_camera_name 冲突需转译为业务错误:
// 否则"同一摄像头下计划重名"会被上层当成数据库故障返回 500.
func (m *recordPlanModel) Create(ctx context.Context, p *RecordPlan) error {
	err := m.db.WithContext(ctx).Create(p).Error
	if err != nil {
		if isDuplicateEntry(err) {
			return ErrRecordPlanDuplicate
		}
		return err
	}
	return nil
}

func (m *recordPlanModel) FindByID(ctx context.Context, tenantID, id int64) (*RecordPlan, error) {
	var p RecordPlan
	err := m.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRecordPlanNotFound
		}
		return nil, err
	}
	return &p, nil
}

// Update 增量更新, 只写 patch 中非 nil 的字段.
func (m *recordPlanModel) Update(ctx context.Context, tenantID, id int64, patch RecordPlanPatch) error {
	fields := map[string]interface{}{}
	if patch.Name != nil {
		fields["name"] = *patch.Name
	}
	if patch.Strategy != nil {
		fields["strategy"] = *patch.Strategy
	}
	if patch.DaysOfWeek != nil {
		fields["days_of_week"] = *patch.DaysOfWeek
	}
	if patch.StartMinute != nil {
		fields["start_minute"] = *patch.StartMinute
	}
	if patch.EndMinute != nil {
		fields["end_minute"] = *patch.EndMinute
	}
	if patch.RetentionDays != nil {
		fields["retention_days"] = *patch.RetentionDays
	}
	if patch.Status != nil {
		fields["status"] = *patch.Status
	}
	if len(fields) == 0 {
		// 空 patch 视为无操作: 返回"不存在"让上层给出可读文案, 而不是静默 UPDATE 成空数据.
		return ErrRecordPlanNotFound
	}
	fields["updated_at"] = time.Now()

	res := m.db.WithContext(ctx).Model(&RecordPlan{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRecordPlanNotFound
	}
	return nil
}

// Delete 删除计划.
//
// 为什么可以物理删除(不软删): 本表不承载历史事实, 只承载"当前应录什么"。
// 删除计划不会让已录视频消失(媒体在网关侧), 也不会让回放结果里的历史窗口无解
// —— 历史能否回放由 retention_days 与当时的计划共同决定, 计划被删后不再产生新窗口。
func (m *recordPlanModel) Delete(ctx context.Context, tenantID, id int64) error {
	res := m.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&RecordPlan{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRecordPlanNotFound
	}
	return nil
}

// DeleteByCamera 删除指定摄像头下的全部计划.
//
// 一条 DELETE 而不是"先 List 再逐条 Delete": 两步之间新创建的计划会漏掉,
// 而这类漏网之鱼正是最难被发现的那一批(看不见、查不到、只在继承时冒出来)。
func (m *recordPlanModel) DeleteByCamera(ctx context.Context, tenantID, cameraID int64) (int64, error) {
	res := m.db.WithContext(ctx).
		Where("camera_id = ? AND tenant_id = ?", cameraID, tenantID).
		Delete(&RecordPlan{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

func (m *recordPlanModel) List(ctx context.Context, f RecordPlanListFilter) ([]*RecordPlan, int64, error) {
	tx := m.db.WithContext(ctx).Model(&RecordPlan{}).Where("tenant_id = ?", f.TenantID)
	if f.CameraID != 0 {
		tx = tx.Where("camera_id = ?", f.CameraID)
	}
	if f.Status != nil {
		tx = tx.Where("status = ?", *f.Status)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := normalizePage(f.Page, f.PageSize)
	var list []*RecordPlan
	if err := tx.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabledByCamera 取某摄像头的启用计划.
func (m *recordPlanModel) ListEnabledByCamera(ctx context.Context, tenantID, cameraID int64) ([]*RecordPlan, error) {
	var list []*RecordPlan
	err := m.db.WithContext(ctx).
		Where("tenant_id = ? AND camera_id = ? AND status = ?",
			tenantID, cameraID, RecordPlanStatusEnabled).
		Order("id ASC").Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
