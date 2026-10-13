// Package model 知识库四张表的读写.
//
// 约定: 所有查询都带 tenant_id 条件 —— 与项目 L2 的数据隔离口径一致.
// 只有"写入切片"需要事务(删除旧切片 + 插新切片 + 更新切片数), 其余都是单语句.
package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ---------- 表结构 ----------

// KbDocument 知识库文档.
type KbDocument struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	TenantID   uint64    `gorm:"column:tenant_id;not null;default:0"`
	Title      string    `gorm:"column:title;type:varchar(200);not null"`
	Source     string    `gorm:"column:source;type:varchar(300);not null;default:''"`
	Category   string    `gorm:"column:category;type:varchar(64);not null;default:''"`
	Content    string    `gorm:"column:content;type:mediumtext;not null"`
	ChunkCount int       `gorm:"column:chunk_count;not null;default:0"`
	Status     int       `gorm:"column:status;not null;default:1"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (KbDocument) TableName() string { return "kb_document" }

// KbChunk 文档切片(检索最小单位).
type KbChunk struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	TenantID   uint64    `gorm:"column:tenant_id;not null;default:0"`
	DocumentID uint64    `gorm:"column:document_id;not null"`
	Seq        int       `gorm:"column:seq;not null;default:0"`
	Heading    string    `gorm:"column:heading;type:varchar(200);not null;default:''"`
	Content    string    `gorm:"column:content;type:text;not null"`
	CharLen    int       `gorm:"column:char_len;not null;default:0"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (KbChunk) TableName() string { return "kb_chunk" }

// KbQaLog 问答日志.
type KbQaLog struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	TenantID  uint64    `gorm:"column:tenant_id;not null;default:0"`
	UserID    uint64    `gorm:"column:user_id;not null;default:0"`
	Question  string    `gorm:"column:question;type:varchar(500);not null"`
	Answer    string    `gorm:"column:answer;type:mediumtext;not null"`
	Mode      string    `gorm:"column:mode;type:varchar(16);not null;default:''"`
	Citations string    `gorm:"column:citations;type:json"`
	TopScore  float64   `gorm:"column:top_score;not null;default:0"`
	ElapsedMs int64     `gorm:"column:elapsed_ms;not null;default:0"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (KbQaLog) TableName() string { return "kb_qa_log" }

// KbFeedback 问答反馈.
type KbFeedback struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement;column:id"`
	TenantID  uint64    `gorm:"column:tenant_id;not null;default:0"`
	QaID      uint64    `gorm:"column:qa_id;not null"`
	Vote      int       `gorm:"column:vote;not null;default:1"`
	Comment   string    `gorm:"column:comment;type:varchar(500);not null;default:''"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (KbFeedback) TableName() string { return "kb_feedback" }

// ---------- 读写方法 ----------

// KBModel 四张表的统一入口.
type KBModel struct{ db *gorm.DB }

func NewKBModel(db *gorm.DB) *KBModel { return &KBModel{db: db} }

// DB 暴露底层连接(ingest 脚本与测试用).
func (m *KBModel) DB() *gorm.DB { return m.db }

// SaveDocument 保存文档并重建切片.
//
// 幂等语义: 同一 (tenant, title, source) 视为"同一份材料的更新" —— 覆盖原文并**重建切片**,
// 避免同一份文档在库里出现多份导致引用重复、评分被稀释。
// 用事务: 删旧切片与插新切片必须同生共死, 否则中途失败会留下"半篇"文档。
func (m *KBModel) SaveDocument(ctx context.Context, doc *KbDocument, chunks []KbChunk) (uint64, error) {
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old KbDocument
		// 用 Find + RowsAffected 判断存在性, 而不是 First + ErrRecordNotFound:
		// 后者在"首次录入"这种正常路径上会打一条 error 级日志, 演示时看着像出错了。
		res := tx.Where("tenant_id = ? AND title = ? AND source = ?", doc.TenantID, doc.Title, doc.Source).
			Limit(1).Find(&old)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			doc.ID = old.ID
			doc.CreatedAt = old.CreatedAt
			if err := tx.Model(&KbDocument{}).Where("id = ? AND tenant_id = ?", old.ID, doc.TenantID).
				Updates(map[string]any{
					"category":    doc.Category,
					"content":     doc.Content,
					"chunk_count": len(chunks),
					"status":      1,
				}).Error; err != nil {
				return err
			}
			if err := tx.Where("document_id = ? AND tenant_id = ?", old.ID, doc.TenantID).
				Delete(&KbChunk{}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(doc).Error; err != nil {
			return err
		}
		for i := range chunks {
			chunks[i].TenantID = doc.TenantID
			chunks[i].DocumentID = doc.ID
		}
		if len(chunks) > 0 {
			if err := tx.Create(&chunks).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return doc.ID, err
}

// ListDocuments 文档列表.
func (m *KBModel) ListDocuments(ctx context.Context, tenantID uint64, category string, page, pageSize int) ([]KbDocument, int64, error) {
	q := m.db.WithContext(ctx).Model(&KbDocument{}).Where("tenant_id = ? AND status = 1", tenantID)
	if category != "" {
		q = q.Where("category = ?", category)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []KbDocument
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// DeleteDocument 删除文档及其切片, 返回删除的切片数.
func (m *KBModel) DeleteDocument(ctx context.Context, tenantID, id uint64) (int64, error) {
	var deleted int64
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("document_id = ? AND tenant_id = ?", id, tenantID).Delete(&KbChunk{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		// 软删: 置 status=2 而不是物理删除 —— 历史问答的引用还能回到原文
		return tx.Model(&KbDocument{}).Where("id = ? AND tenant_id = ?", id, tenantID).
			Update("status", 2).Error
	})
	return deleted, err
}

// DocumentExists 文档是否存在(且属于该租户).
func (m *KBModel) DocumentExists(ctx context.Context, tenantID, id uint64) (bool, error) {
	var n int64
	err := m.db.WithContext(ctx).Model(&KbDocument{}).
		Where("id = ? AND tenant_id = ? AND status = 1", id, tenantID).Count(&n).Error
	return n > 0, err
}

// ScanChunks 载入该租户的全部切片(带文档标题/出处), 供检索打分.
// 排序固定, 保证同样输入的打分结果可复现(便于单测断言)。
func (m *KBModel) ScanChunks(ctx context.Context, tenantID uint64, limit int) ([]ChunkRow, error) {
	var rows []ChunkRow
	err := m.db.WithContext(ctx).Table("kb_chunk c").
		Select("c.id, c.document_id, c.seq, c.heading, c.content, d.title, d.source, d.category").
		Joins("JOIN kb_document d ON d.id = c.document_id AND d.tenant_id = c.tenant_id").
		Where("c.tenant_id = ? AND d.status = 1", tenantID).
		Order("c.document_id ASC, c.seq ASC").
		Limit(limit).Scan(&rows).Error
	return rows, err
}

// ChunkRow 检索用的扁平行(切片 + 所属文档信息).
type ChunkRow struct {
	ID         uint64 `gorm:"column:id"`
	DocumentID uint64 `gorm:"column:document_id"`
	Seq        int    `gorm:"column:seq"`
	Heading    string `gorm:"column:heading"`
	Content    string `gorm:"column:content"`
	Title      string `gorm:"column:title"`
	Source     string `gorm:"column:source"`
	Category   string `gorm:"column:category"`
}

// InsertQaLog 写问答日志, 返回自增 id.
func (m *KBModel) InsertQaLog(ctx context.Context, log *KbQaLog) (uint64, error) {
	if err := m.db.WithContext(ctx).Create(log).Error; err != nil {
		return 0, err
	}
	return log.ID, nil
}

// ListQaLogs 问答日志(倒序).
func (m *KBModel) ListQaLogs(ctx context.Context, tenantID uint64, page, pageSize int) ([]KbQaLog, int64, error) {
	q := m.db.WithContext(ctx).Model(&KbQaLog{}).Where("tenant_id = ?", tenantID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []KbQaLog
	err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// QaExists 该问答是否属于此租户(反馈前校验, 防越权给别人的问答投票).
func (m *KBModel) QaExists(ctx context.Context, tenantID, qaID uint64) (bool, error) {
	var n int64
	err := m.db.WithContext(ctx).Model(&KbQaLog{}).
		Where("id = ? AND tenant_id = ?", qaID, tenantID).Count(&n).Error
	return n > 0, err
}

// InsertFeedback 写反馈.
func (m *KBModel) InsertFeedback(ctx context.Context, fb *KbFeedback) error {
	return m.db.WithContext(ctx).Create(fb).Error
}

// Stats 简单统计(自检与演示用).
func (m *KBModel) Stats(ctx context.Context, tenantID uint64) (docs, chunks int64, err error) {
	if err = m.db.WithContext(ctx).Model(&KbDocument{}).
		Where("tenant_id = ? AND status = 1", tenantID).Count(&docs).Error; err != nil {
		return
	}
	err = m.db.WithContext(ctx).Model(&KbChunk{}).Where("tenant_id = ?", tenantID).Count(&chunks).Error
	return
}
