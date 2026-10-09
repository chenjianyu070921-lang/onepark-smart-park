package main

import (
	"flag"
	"fmt"

	"onepark/app/energy-agent-service/internal/config"
	"onepark/app/energy-agent-service/internal/handler"
	"onepark/app/energy-agent-service/internal/job"
	"onepark/app/energy-agent-service/internal/svc"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/energyagent-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	// HTTP 接口 + 定时巡检一起拉起, 缺一个就整体退出
	group := service.NewServiceGroup()
	defer group.Stop()
	group.Add(server)
	if c.InspectJob.Enable {
		group.Add(job.NewInspectJob(ctx, c.InspectJob.Hour))
	}

	fmt.Printf("Starting energy-agent server at %s:%d, llm=%s...\n",
		c.Host, c.Port, llmName(c))
	group.Start()
}

// llmName 启动时告诉你这次走的是真模型还是兜底, 免得演示时才发现 key 没生效
func llmName(c config.Config) string {
	if !c.LLM.Enable || c.LLM.APIKey == "" {
		return "mock(未配置, 用规则原文)"
	}
	return c.LLM.Model
}
