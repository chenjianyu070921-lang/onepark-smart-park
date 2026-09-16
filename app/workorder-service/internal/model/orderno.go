// 工单号生成与唯一键冲突重试.
//
// 为什么需要重试: 工单号 = 日期 + 4 位随机数(而非自增序列), 目的是不向外部
// (租户端/通知文案)泄露单量; 随机必然存在撞号概率, 落库时依赖 work_order 的
// uk_order_no 唯一索引兜底 —— 撞号(MySQL 1062)则换号重试.
// "生成→校验→插入"两步走在并发下不可靠, 唯一约束才是唯一可信的判重手段.
package model

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// OrderNoPattern 工单号格式: WO-YYYYMMDD-4位随机数(0000-9999).
const OrderNoPattern = `^WO-\d{8}-\d{4}$`

// MaxOrderNoRetries 随机工单号撞 uk_order_no 唯一键时的最大尝试次数.
// 单日随机空间 10000, 撞号概率极低; 耗尽说明当天号高度饱和, 应人工介入.
const MaxOrderNoRetries = 5

// NewOrderNo 生成工单号 WO-YYYYMMDD-XXXX.
// 使用 crypto/rand 而非 math/rand: 不需要播种, 且多实例间无相关性感知.
// crypto/rand 读取失败极罕见(熵源故障), 退化为时间戳尾数, 保证总能返回可用号.
func NewOrderNo() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return fmt.Sprintf("WO-%s-%04d", time.Now().Format("20060102"), time.Now().UnixNano()%10000)
	}
	return fmt.Sprintf("WO-%s-%04d", time.Now().Format("20060102"), n.Int64())
}

// ErrRetryExhausted 唯一键冲突重试次数耗尽.
var ErrRetryExhausted = errors.New("唯一键冲突重试次数耗尽")

// RetryOnDuplicate 执行 fn, 遇唯一键冲突(1062)重试, 最多 attempts 次.
//
// 语义: fn 每次调用前应重新生成随机业务号(工单号/其它随机键);
// 非 1062 错误(网络/字段非法等)不重试, 原样返回.
// attempts 下限为 1, 传 0 或负数视为只尝试一次.
func RetryOnDuplicate(attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if IsDuplicateEntry(lastErr) {
			continue // 撞号, 换号再来
		}
		return lastErr
	}
	return fmt.Errorf("%w(共 %d 次): %v", ErrRetryExhausted, attempts, lastErr)
}

// IsDuplicateEntry 判断错误是否为 MySQL 唯一键冲突(1062).
// 通过错误文本匹配而非引入 mysql 驱动类型: gorm 会透出驱动原始错误,
// 其文本形如 `Error 1062: Duplicate entry 'WO-...' for key 'work_order.uk_order_no'`,
// 其中包含冲突的约束名, 调用方可用 strings.Contains 区分撞了哪个唯一键.
func IsDuplicateEntry(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// MySQL: Error 1062 / "Duplicate entry '...' for key '...'";
	// SQLite(单测/本地): "UNIQUE constraint failed: ..."。两者都识别,
	// 保证撞唯一键的判定在 MySQL 生产与 SQLite 单测下行为一致。
	return strings.Contains(msg, "1062") ||
		strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}
