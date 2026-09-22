// grpcping 对 leasing 的 gRPC 服务做一次**真实 RPC** 探活.
//
// 为什么不是"TCP 端口能连上就行": 端口开着只说明进程在监听, 不代表 RPC 真能用 ——
// 依赖没连上、初始化失败、序列化不匹配都可能"端口开着但调用必失败"。
// 联调日一个个手点太慢, 这条命令给一句话结论。
//
// 用法:
//
//	go run ./app/leasing-service/tools/grpcping -addr 127.0.0.1:9051
//
// 退出码: 0 = Ping 成功; 1 = 失败(含原因)。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "onepark/proto/common"
	leasingpb "onepark/proto/leasing"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9051", "leasing gRPC 地址(host:port)")
	timeout := flag.Duration("timeout", 5*time.Second, "单次探活超时")
	flag.Parse()

	// 刻意用 NewClient + WithTransportCredentials(insecure): 内网明文, 与各服务 main 的拨号方式一致。
	// NewClient 是惰性连接(不会在这里就失败), 真正的连通性由下面那次 Ping 证明。
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[grpcping] 构造客户端失败: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	start := time.Now()
	if _, err := leasingpb.NewLeasingServiceClient(conn).Ping(ctx, &commonpb.Empty{}); err != nil {
		fmt.Fprintf(os.Stderr, "[grpcping] %s Ping 失败: %v\n", *addr, err)
		os.Exit(1)
	}
	fmt.Printf("[grpcping] %s Ping OK (耗时 %s)\n", *addr, time.Since(start).Round(time.Millisecond))
}
