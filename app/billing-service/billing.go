package main

import (
	"flag"
	"fmt"

	"onepark/app/billing-service/internal/config"
	"onepark/app/billing-service/internal/handler"
	"onepark/app/billing-service/internal/job"
	"onepark/app/billing-service/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/billing-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// HTTP 接口 + 定时出账任务一起拉起, 缺一个就整体退出
	group := service.NewServiceGroup()
	defer group.Stop()
	group.Add(server)
	if c.BillJob.Enable {
		group.Add(job.NewBillJob(ctx, c.BillJob.DayOfMonth))
	}

	fmt.Printf("Starting billing server at %s:%d...\n", c.Host, c.Port)
	group.Start()
}
