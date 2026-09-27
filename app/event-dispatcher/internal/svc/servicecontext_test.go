package svc

import (
	"testing"

	"onepark/app/event-dispatcher/internal/config"
)

// TestBuildResolverRequiredMissingDSN fail-closed: DSN 空且 ArchiveRequired=true 必须报错.
func TestBuildResolverRequiredMissingDSN(t *testing.T) {
	r, err := buildResolver(config.Config{ArchiveRequired: true})
	if err == nil {
		t.Fatal("ArchiveRequired=true 且 DSN 为空时应拒绝启动")
	}
	if r != nil {
		t.Fatalf("出错时 resolver 应为 nil, got %v", r)
	}
}

// TestBuildResolverOptionalMissingDSN 显式降级: DSN 空且 ArchiveRequired=false 放行 nil resolver.
func TestBuildResolverOptionalMissingDSN(t *testing.T) {
	r, err := buildResolver(config.Config{ArchiveRequired: false})
	if err != nil {
		t.Fatalf("ArchiveRequired=false 时不应报错: %v", err)
	}
	if r != nil {
		t.Fatalf("DSN 为空时 resolver 应为 nil(降级模式), got %v", r)
	}
}

// 注: DSN 非空的成功路径不在此覆盖 —— gorm mysql driver 初始化时会 SELECT VERSION()
// 真实拨号, 单测环境无 MySQL; 该路径由联调验证.
