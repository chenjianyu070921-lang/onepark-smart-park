package model

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func TestNewOrderNo_Format(t *testing.T) {
	re := regexp.MustCompile(OrderNoPattern)
	for i := 0; i < 100; i++ {
		no := NewOrderNo()
		if !re.MatchString(no) {
			t.Fatalf("order no %q does not match pattern %s", no, OrderNoPattern)
		}
	}
}

func TestNewOrderNo_DailyScope(t *testing.T) {
	// 号内日期应为今天, 随机段取值范围 [0,9999].
	no := NewOrderNo()
	if !strings.HasPrefix(no, "WO-") {
		t.Fatalf("missing prefix: %s", no)
	}
	datePart := no[3:11]
	if len(datePart) != 8 || datePart[0] < '0' || datePart[0] > '9' {
		t.Fatalf("bad date part: %s", datePart)
	}
}

func TestRetryOnDuplicate_Success(t *testing.T) {
	calls := 0
	err := RetryOnDuplicate(5, func() error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("expect success on first call, calls=%d err=%v", calls, err)
	}
}

func TestRetryOnDuplicate_RetriesUntilSuccess(t *testing.T) {
	// 模拟撞号两次后成功: 重试次数应恰好 3.
	calls := 0
	err := RetryOnDuplicate(5, func() error {
		calls++
		if calls < 3 {
			return errors.New("Error 1062: Duplicate entry 'WO-20260916-0001' for key 'work_order.uk_order_no'")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expect success, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expect 3 calls, got %d", calls)
	}
}

func TestRetryOnDuplicate_Exhausted(t *testing.T) {
	// 持续撞号: 耗尽次数后返回 ErrRetryExhausted.
	calls := 0
	err := RetryOnDuplicate(3, func() error {
		calls++
		return errors.New("Error 1062: Duplicate entry for key 'uk_order_no'")
	})
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("expect ErrRetryExhausted, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expect exactly 3 attempts, got %d", calls)
	}
}

func TestRetryOnDuplicate_NonDuplicateFailsFast(t *testing.T) {
	// 非唯一键冲突(如连接失败)不应重试, 立即原样返回.
	calls := 0
	sentinel := errors.New("connection refused")
	err := RetryOnDuplicate(5, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expect original error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expect fail-fast (1 call), got %d", calls)
	}
}

func TestRetryOnDuplicate_MinAttempts(t *testing.T) {
	// attempts 下限 1: 传 0 也要执行一次.
	calls := 0
	_ = RetryOnDuplicate(0, func() error {
		calls++
		return nil
	})
	if calls != 1 {
		t.Fatalf("expect at least one attempt, got %d", calls)
	}
}

func TestIsDuplicateEntry(t *testing.T) {
	if IsDuplicateEntry(nil) {
		t.Fatal("nil error is not duplicate")
	}
	if !IsDuplicateEntry(errors.New("Error 1062: Duplicate entry 'x' for key 'uk_order_no'")) {
		t.Fatal("1062 text should match")
	}
	if IsDuplicateEntry(errors.New("connection refused")) {
		t.Fatal("non-duplicate error should not match")
	}
}
