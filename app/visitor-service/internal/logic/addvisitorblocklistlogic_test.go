package logic

import (
	"testing"

	"onepark/app/visitor-service/internal/types"
)

// TestBuildBlocklistDedupWhere 黑名单去重条件构造:
// 同租户已生效记录中, 手机号或身份证任一命中即拦截(OR 语义).
func TestBuildBlocklistDedupWhere(t *testing.T) {
	w, a := buildBlocklistDedupWhere(&types.AddVisitorBlocklistReq{Phone: "13800000000"})
	if w != "phone = ?" || len(a) != 1 || a[0] != "13800000000" {
		t.Errorf("仅手机号条件错误: where=%q args=%v", w, a)
	}
	w, a = buildBlocklistDedupWhere(&types.AddVisitorBlocklistReq{IdNo: "X123"})
	if w != "id_no = ?" || len(a) != 1 || a[0] != "X123" {
		t.Errorf("仅身份证条件错误: where=%q args=%v", w, a)
	}
	w, a = buildBlocklistDedupWhere(&types.AddVisitorBlocklistReq{Phone: "1", IdNo: "2"})
	if w != "phone = ? OR id_no = ?" || len(a) != 2 {
		t.Errorf("双维度 OR 条件错误: where=%q args=%v", w, a)
	}
}

// TestUnixPtrBL 秒级时间戳转 *time.Time: 0/负数降级 nil, 正数返回时间(对齐黑名单生效时间窗).
func TestUnixPtrBL(t *testing.T) {
	if unixPtrBL(0) != nil {
		t.Error("0 应返回 nil")
	}
	if unixPtrBL(-5) != nil {
		t.Error("负数应返回 nil")
	}
	if unixPtrBL(1000000000) == nil {
		t.Error("正数应返回非 nil 时间")
	}
}
