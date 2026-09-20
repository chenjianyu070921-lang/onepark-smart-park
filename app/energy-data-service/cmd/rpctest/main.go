// 常驻联调工具: gRPC 接口没法用 ApiPost 直接测(走的是 HTTP/2, 和浏览器那套不一样),
// 所以用这个小客户端调接口54(GetDailyReport), 以后新增 gRPC 接口也往这里加用例
// 用法: 先启动服务(go run . -f etc/energydata-api.yaml), 再执行 go run ./cmd/rpctest
package main

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	energypb "onepark/proto/energy"
)

func main() {
	conn, err := grpc.NewClient("127.0.0.1:9061", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Println("连接失败(服务启动了吗?):", err)
		return
	}
	defer conn.Close()

	client := energypb.NewEnergyDataServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []energypb.GetDailyReportRequest{
		{},                                        // 全园区 + 今天
		{ZoneId: "A栋"},                            // 只看 A栋
		{Date: "2026-09-14"},                      // 昨天没数据
	}
	for i, req := range cases {
		resp, err := client.GetDailyReport(ctx, &req)
		if err != nil {
			fmt.Printf("[%d] 失败: %v\n", i, err)
			continue
		}
		fmt.Printf("[%d] 日期=%s 区域=%q 总用量=%.2f度 最新数据时间=%s\n",
			i, resp.GetDate(), resp.GetZoneId(), resp.GetTotalUsageKwh(), resp.GetUpdatedAt())
	}
}
