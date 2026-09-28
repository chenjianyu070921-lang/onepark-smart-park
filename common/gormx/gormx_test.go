package gormx

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
)

// TestIsDuplicateKey 重复键判定: 直接 gorm.ErrDuplicatedKey、经 %w 包装的链、以及非重复错误/ nil 均应正确分类.
// 该判定是 visitor_blocklist / monthly_card 并发插入竞态的幂等回退依据(审查问题2/3).
func TestIsDuplicateKey(t *testing.T) {
	if !IsDuplicateKey(gorm.ErrDuplicatedKey) {
		t.Error("gorm.ErrDuplicatedKey 应判为重复键")
	}
	// 经 %w 包装后仍应被 errors.Is 识别
	wrapped := fmt.Errorf("wrap: %w", gorm.ErrDuplicatedKey)
	if !IsDuplicateKey(wrapped) {
		t.Error("经 %w 包装的重复键错误应被识别(并发回退依赖此判定)")
	}
	// 普通错误不应误判
	if IsDuplicateKey(errors.New("some other error")) {
		t.Error("普通错误不应判为重复键")
	}
	// nil 不应 panic 或误判
	if IsDuplicateKey(nil) {
		t.Error("nil 不应判为重复键")
	}
}
