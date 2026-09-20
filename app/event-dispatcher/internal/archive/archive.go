// Package archive 提供设备档案(device_id → tenant_id/zone_id)查询与 TTL 缓存,
// 供 event-dispatcher 在投递 Kafka 前给消息充入租户与区域维度.
package archive

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Profile 设备档案中消息充入所需的两个字段.
type Profile struct {
	TenantID int64
	ZoneID   string
}

// Reader 档案读取抽象, 便于单测替换; 生产实现为 GormReader.
type Reader interface {
	// Get 返回设备档案; ok=false 表示设备不存在(负缓存依据), err 非 nil 表示查询失败(不缓存).
	Get(ctx context.Context, deviceID string) (Profile, bool, error)
}

// GormReader 直连业务库只读查询 device 表.
type GormReader struct {
	db *gorm.DB
}

// deviceRow 仅映射需要的两列, 避免耦合 device-service 的完整模型.
type deviceRow struct {
	TenantID int64  `gorm:"column:tenant_id"`
	ZoneID   string `gorm:"column:zone_id"`
}

// NewGormReader 构造 GORM 档案读取器.
func NewGormReader(db *gorm.DB) *GormReader {
	return &GormReader{db: db}
}

func (g *GormReader) Get(ctx context.Context, deviceID string) (Profile, bool, error) {
	var row deviceRow
	err := g.db.WithContext(ctx).
		Table("device").
		Select("tenant_id", "zone_id").
		Where("device_id = ? AND deleted_at IS NULL", deviceID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Profile{}, false, nil
		}
		return Profile{}, false, err
	}
	return Profile{TenantID: row.TenantID, ZoneID: row.ZoneID}, true, nil
}

// cacheEntry 带 TTL 的缓存条目; notFound=true 为负缓存.
type cacheEntry struct {
	profile  Profile
	notFound bool
	expires  time.Time
}

// Resolver 带 TTL 的档案缓存; 并发安全.
type Resolver struct {
	reader Reader
	ttl    time.Duration
	mu     sync.RWMutex
	cache  map[string]cacheEntry
}

// NewResolver 构造缓存解析器; reader 为 nil 时表示未配置 MySQL,
// 一律零值放行(保持"未配置即降级启动"的仓库约定).
func NewResolver(reader Reader, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Resolver{reader: reader, ttl: ttl, cache: map[string]cacheEntry{}}
}

// Resolve 查询设备档案; ok=false 表示设备不存在, err 非 nil 表示查询失败.
func (r *Resolver) Resolve(ctx context.Context, deviceID string) (Profile, bool, error) {
	if r.reader == nil {
		return Profile{}, false, nil
	}

	r.mu.RLock()
	e, hit := r.cache[deviceID]
	r.mu.RUnlock()
	if hit && time.Now().Before(e.expires) {
		return e.profile, !e.notFound, nil
	}

	p, ok, err := r.reader.Get(ctx, deviceID)
	if err != nil {
		// 查询失败不缓存, 下条消息重试
		return Profile{}, false, err
	}
	entry := cacheEntry{profile: p, expires: time.Now().Add(r.ttl)}
	if !ok {
		entry.notFound = true
		entry.profile = Profile{}
	}
	r.mu.Lock()
	r.cache[deviceID] = entry
	r.mu.Unlock()
	return entry.profile, !entry.notFound, nil
}
