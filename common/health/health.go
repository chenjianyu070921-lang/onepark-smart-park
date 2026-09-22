// Package health 提供统一的 HTTP 健康检查实现, 供各 go-zero REST 服务复用.
// 设计原则: 依赖缺失(nil)时跳过对应探测, 不影响存活判定; 探测失败标记为 degraded 而非 fatal.
//
// 提供三类端点, 适配 K8s/Docker 探针语义:
//   - Liveness : 仅判断进程是否假死, 不依赖外部组件; 失败即重启 Pod.
//   - Readiness: 探测 MySQL/Redis 及自定义依赖(如 Kafka); 未就绪时摘流量, 但不杀 Pod.
//   - Handler  : 兼容旧用法(不区分存活/就绪), 同时探 MySQL+Redis.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"onepark/common/gormx"
	"onepark/common/redisx"
)

// Component 描述单个被探测依赖的健康状态.
type Component struct {
	Name   string `json:"name"`
	Ok     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Response 健康检查响应体, 兼容 K8s/Docker 探针与人工排查.
type Response struct {
	Status     string      `json:"status"` // ok | degraded
	Timestamp  string      `json:"timestamp"`
	Components []Component `json:"components,omitempty"`
}

// Probe 自定义依赖探测(如 Kafka), 返回 (依赖名, 是否健康, 明细).
// 与 MySQL/Redis 共用同一 degraded 判定与响应结构, 便于在 Readiness 中统一聚合.
// 例: 探测 Kafka 连通性可写成
//
//	func(ctx context.Context) (string, bool, string) {
//	    if err := producer.Ping(ctx); err != nil {
//	        return "kafka", false, err.Error()
//	    }
//	    return "kafka", true, ""
//	}
type Probe func(ctx context.Context) (name string, ok bool, detail string)

// Handler 兼容旧用法: 同时探测 MySQL+Redis, 不区分存活/就绪.
// 新服务请改用 Liveness + Readiness 分别挂载 /api/healthz 与 /api/readyz.
func Handler(db *gormx.DB, rdb *redisx.Client) http.HandlerFunc {
	return Readiness(db, rdb)
}

// Liveness 存活探针: 进程已启动即视为健康, 不探测任何外部依赖.
// 用于 K8s livenessProbe: 仅判断进程是否假死, 失败即重启 Pod.
func Liveness() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, Response{Status: "ok", Timestamp: time.Now().Format(time.RFC3339)})
	}
}

// Readiness 就绪探针: 探测 MySQL/Redis 及自定义依赖(probes, 如 Kafka).
// db 为 nil 跳过 MySQL; rdb 为 nil 跳过 Redis; probes 为空则只探已配置依赖.
// 任一已配置依赖探测失败, 整体状态置为 degraded, 但 HTTP 仍返回 200(存活),
// 避免依赖抖动导致容器被误杀; 依赖级不可用由 components 明细暴露.
func Readiness(db *gormx.DB, rdb *redisx.Client, probes ...Probe) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		comps := make([]Component, 0, 2+len(probes))

		if db != nil {
			comps = append(comps, probeMySQL(ctx, db))
		}
		if rdb != nil {
			comps = append(comps, probeRedis(ctx, rdb))
		}
		for _, p := range probes {
			name, ok, detail := p(ctx)
			comps = append(comps, Component{Name: name, Ok: ok, Detail: detail})
		}

		overall := "ok"
		for _, c := range comps {
			if !c.Ok {
				overall = "degraded"
			}
		}
		writeJSON(w, Response{
			Status:     overall,
			Timestamp:  time.Now().Format(time.RFC3339),
			Components: comps,
		})
	}
}

// probeMySQL 探测 MySQL 连通性: 取底层 *sql.DB 执行 Ping.
func probeMySQL(ctx context.Context, db *gormx.DB) Component {
	sqlDB, err := db.DB()
	if err != nil {
		return Component{Name: "mysql", Ok: false, Detail: err.Error()}
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return Component{Name: "mysql", Ok: false, Detail: err.Error()}
	}
	return Component{Name: "mysql", Ok: true}
}

// probeRedis 探测 Redis 连通性: 执行 Ping(ctx).
func probeRedis(ctx context.Context, rdb *redisx.Client) Component {
	if err := rdb.Ping(ctx).Err(); err != nil {
		return Component{Name: "redis", Ok: false, Detail: err.Error()}
	}
	return Component{Name: "redis", Ok: true}
}

// writeJSON 以统一响应体 {status,timestamp,components} 输出健康检查结果.
func writeJSON(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
