// Package health 提供统一的 HTTP 健康检查实现, 供各 go-zero REST 服务复用.
// 设计原则: 依赖缺失(nil)时跳过对应探测, 不影响存活判定; 探测失败标记为 degraded 而非 fatal.
package health

import (
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

// Handler 返回一个 HTTP 健康检查处理函数.
//   - db 为 nil 时跳过 MySQL 探测(无 DB 依赖的服务, 如纯网关).
//   - rdb 为 nil 时跳过 Redis 探测(未配置 Redis 的服务).
//
// 探测逻辑: MySQL 取底层 *sql.DB 执行 Ping; Redis 执行 Ping(ctx).
// 任一已配置依赖探测失败, 整体状态置为 degraded, 但 HTTP 仍返回 200(存活),
// 避免依赖抖动导致容器被误杀; 依赖级不可用由 components 明细暴露.
func Handler(db *gormx.DB, rdb *redisx.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		comps := make([]Component, 0, 2)

		if db != nil {
			if sqlDB, err := db.DB(); err != nil {
				comps = append(comps, Component{Name: "mysql", Ok: false, Detail: err.Error()})
			} else if err := sqlDB.Ping(); err != nil {
				comps = append(comps, Component{Name: "mysql", Ok: false, Detail: err.Error()})
			} else {
				comps = append(comps, Component{Name: "mysql", Ok: true})
			}
		}

		if rdb != nil {
			if err := rdb.Ping(r.Context()).Err(); err != nil {
				comps = append(comps, Component{Name: "redis", Ok: false, Detail: err.Error()})
			} else {
				comps = append(comps, Component{Name: "redis", Ok: true})
			}
		}

		overall := "ok"
		for _, c := range comps {
			if !c.Ok {
				overall = "degraded"
			}
		}

		resp := Response{
			Status:     overall,
			Timestamp:  time.Now().Format(time.RFC3339),
			Components: comps,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
