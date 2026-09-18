package archive

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubReader struct {
	profiles map[string]Profile
	err      error
	calls    int
}

func (s *stubReader) Get(ctx context.Context, deviceID string) (Profile, bool, error) {
	s.calls++
	if s.err != nil {
		return Profile{}, false, s.err
	}
	p, ok := s.profiles[deviceID]
	return p, ok, nil
}

// TestResolverCacheHit 缓存命中时不得重复查库(30s TTL 内).
func TestResolverCacheHit(t *testing.T) {
	s := &stubReader{profiles: map[string]Profile{"d1": {TenantID: 7, ZoneID: "zone-a"}}}
	r := NewResolver(s, time.Minute)

	for i := 0; i < 3; i++ {
		p, ok, err := r.Resolve(context.Background(), "d1")
		if err != nil || !ok || p.TenantID != 7 || p.ZoneID != "zone-a" {
			t.Fatalf("第 %d 次解析异常: p=%+v ok=%v err=%v", i, p, ok, err)
		}
	}
	if s.calls != 1 {
		t.Fatalf("缓存命中仍查库, calls=%d", s.calls)
	}
}

// TestResolverUnknownDevice 设备不存在: ok=false 且结果负缓存, 不反复查库.
func TestResolverUnknownDevice(t *testing.T) {
	s := &stubReader{profiles: map[string]Profile{}}
	r := NewResolver(s, time.Minute)

	_, ok, err := r.Resolve(context.Background(), "ghost")
	if err != nil || ok {
		t.Fatalf("未知设备应返回 ok=false err=nil, got ok=%v err=%v", ok, err)
	}
	_, _, _ = r.Resolve(context.Background(), "ghost")
	if s.calls != 1 {
		t.Fatalf("未知设备未负缓存, calls=%d", s.calls)
	}
}

// TestResolverDBError 查库报错不缓存, 下次重试.
func TestResolverDBError(t *testing.T) {
	s := &stubReader{err: errors.New("db down")}
	r := NewResolver(s, time.Minute)

	_, _, err := r.Resolve(context.Background(), "d1")
	if err == nil {
		t.Fatal("DB 错误应上抛")
	}
	_, _, _ = r.Resolve(context.Background(), "d1")
	if s.calls != 2 {
		t.Fatalf("DB 错误不应缓存, calls=%d", s.calls)
	}
}

// TestResolverNilReader 未配置 MySQL 时零值放行(向后兼容: 消息不带租户也照常转发).
func TestResolverNilReader(t *testing.T) {
	r := NewResolver(nil, time.Minute)
	p, ok, err := r.Resolve(context.Background(), "d1")
	if err != nil || ok || p.TenantID != 0 || p.ZoneID != "" {
		t.Fatalf("nil reader 应零值放行: p=%+v ok=%v err=%v", p, ok, err)
	}
}
