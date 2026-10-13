// Package main 企业知识库服务入口.
//
// 一句话: 把园区自己的材料变成"可问答"的知识库, 回答**必须带出处**, 检索不到就明说没有依据.
//
// 三件设计上的事(答辩会被问, 先写在入口这里):
//  1. **回答的锚点在检索, 不在模型**: 模型只负责把检索到的片段组织成人话;
//     LLM 关掉/没 key/超时, 退化成"抽取式回答"(直接引用最相关的原文), 功能不消失。
//     这与 M4 智能体服务"让确定性的归确定性"是同一条原则。
//  2. **检索实现是可替换的**: 现在用 MySQL + Go 侧 BM25(本机零依赖, 答辩不受网络影响),
//     向量检索只需实现同一个 Retriever 接口, 上层逻辑不动(见 internal/retriever)。
//  3. **没有依据时不编**: 命中分数低于阈值直接返回 no_hit, 并如实告诉用户"知识库里没有"。
//     这是企业知识库能不能被信任的分界线 —— 宁可拒答, 不要幻觉。
package main

import (
	"flag"
	"fmt"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/rest"

	"onepark/app/kb-service/internal/config"
	"onepark/app/kb-service/internal/handler"
	"onepark/app/kb-service/internal/svc"
	"onepark/common/middleware"
	"onepark/common/response"
)

var configFile = flag.String("f", "etc/kb-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	// UseEnv 必填: 配置里的 ${KB_MYSQL_DSN} 之类由环境变量注入
	conf.MustLoad(*configFile, &c, conf.UseEnv())

	// 统一 API 响应体为 {code,msg,data}
	response.Init()

	server := rest.MustNewServer(c.RestConf)
	// 租户/用户身份由网关注入, 本服务只提升到 ctx(直连时缺头 -> 身份为 0)
	server.Use(middleware.IdentityFromHeader)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
