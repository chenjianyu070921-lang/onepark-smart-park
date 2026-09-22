package logic

import (
	"testing"

	"onepark/app/notice-service/internal/model"
)

// TestCanRecall 撤回前置校验: 仅已发布(2)可撤回(对齐 recallnoticelogic.go 业务约束).
// 草稿(1)未发布、已撤回(3)不可重复撤回.
func TestCanRecall(t *testing.T) {
	if !CanRecall(model.NoticeStatusPublished) {
		t.Error("已发布(2)应可撤回")
	}
	if CanRecall(model.NoticeStatusDraft) {
		t.Error("草稿(1)未发布, 不可撤回")
	}
	if CanRecall(model.NoticeStatusWithdrawn) {
		t.Error("已撤回(3)不可重复撤回")
	}
}
