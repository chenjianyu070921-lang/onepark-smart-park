package checks

import (
	"context"
	"fmt"
	"net"
	"time"
)

// CheckTcp 探活 TCP 端口, 用于 MySQL/Redis/Kafka/EMQX 等.
// 仅做 TCP 拨号, 不引入具体驱动.
func CheckTcp(addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp dial %s: %w", addr, err)
	}
	_ = conn.Close()
	return nil
}
