// Package wsserver 大屏 WebSocket 推送服务(清单 #73)。
//
// 推送由两条路径组成, 缺一不可:
//  1. **周期快照**(本文件): 每 5s 复用 Overview 聚合逻辑构建全量快照并广播 ——
//     前端靠它拿到数据基线, 断线重连后也能恢复;
//  2. **事件增量**(event.go): 消费 Kafka 的告警/工单事件, 到达即广播增量消息,
//     并置脏标记; 快照循环在推送前据此失效聚合缓存, 使下一次聚合读到最新值。
//
// 只有快照会"慢半拍"(30s 缓存可能盖住事件效果), 只有事件则前端无基线。
//
// Overview 自带 30s 租户缓存, 5s 推送基本命中缓存, 成本极低;
// 一旦有事件就把缓存失效掉、重新聚合一次, 实时性与成本兼顾。
//
// 鉴权: 按既定决策走 `?token=`(浏览器 WebSocket 无法自定义 Authorization 头)。
// ⚠️ 开发期暂不校验 token, M6 JWT 网关就绪后必须在 Handler 中补校验 —— 已在确认书中披露。
package wsserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zeromicro/go-zero/core/logx"

	"onepark/app/dashboard-service/internal/logic/dashboard"
	"onepark/app/dashboard-service/internal/svc"
	"onepark/app/dashboard-service/internal/types"
	"onepark/app/dashboard-service/internal/wshub"
)

// pushInterval 快照推送间隔.
const pushInterval = 5 * time.Second

// snapshotMsg 推送给大屏的消息结构.
type snapshotMsg struct {
	Type string              `json:"type"`
	Data *types.OverviewResp `json:"data"`
}

// Handler 返回 WebSocket 升级处理器, 路由为 GET /ws/dashboard.
func Handler(hub *wshub.Hub) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// 大屏前端与服务可能不同源; 开发期放开, 上线前必须收敛为域名白名单
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// TODO(M6): 校验 r.URL.Query().Get("token"), 当前为开发期放行
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade 失败时响应已由 upgrader 写出(如 400), 这里只需返回
			return
		}

		client := wshub.NewClient(hub, conn)
		hub.Register(client)
		go client.WritePump()
		go client.ReadPump()

		logx.WithContext(r.Context()).Infof("[ws] 大屏接入: remote=%s, online=%d",
			r.RemoteAddr, hub.Count())
	}
}

// StartSnapshotPush 周期构建概览快照并广播; 阻塞运行, 应在独立 goroutine 中调用.
//
// dirty 由事件消费者(event.go)置位; 本循环取走后失效聚合缓存再重新聚合 ——
// 这是"事件驱动"真正生效的位置: 不失效就会被 30s 旧缓存盖住。
func StartSnapshotPush(ctx context.Context, svcCtx *svc.ServiceContext, hub *wshub.Hub, dirty *Dirty) {
	logger := logx.WithContext(ctx)
	ticker := time.NewTicker(pushInterval)
	defer ticker.Stop()

	if dirty == nil {
		dirty = NewDirty() // 未启用事件推送时的空实现, 保证调用方无需分支
	}

	logger.Infof("[ws] 快照推送已启动: interval=%s", pushInterval)
	for {
		select {
		case <-ctx.Done():
			logger.Info("[ws] 快照推送已停止")
			return
		case <-ticker.C:
			// 有事件到达 -> 先失效缓存, 让本次快照聚合到最新值。
			if dirty.Take() {
				if n, err := dashboard.InvalidateOverviewCache(ctx, svcCtx); err != nil {
					// 失效失败不中断推送: 下一次快照仍会返回(可能略旧的)数据, 好过不推
					logger.Errorf("[ws] 聚合缓存失效失败: %v", err)
				} else if n > 0 {
					logger.Infof("[ws] 事件触发缓存失效: keys=%d, 本次快照将重新聚合", n)
				}
			}

			// 复用聚合逻辑: 自带逐源降级与 30s 缓存, 任一数据源挂了也不影响推送
			resp, err := dashboard.NewOverviewLogic(ctx, svcCtx).
				Overview(&types.OverviewReq{TenantId: 0})
			if err != nil {
				logger.Errorf("[ws] 构建快照失败: %v", err)
				continue
			}

			payload, err := json.Marshal(snapshotMsg{Type: "snapshot", Data: resp})
			if err != nil {
				logger.Errorf("[ws] 快照序列化失败: %v", err)
				continue
			}

			if n := hub.Count(); n > 0 {
				hub.Broadcast(payload)
				logger.Debugf("[ws] 已推送快照: clients=%d, degraded=%v", n, resp.Degraded)
			}
		}
	}
}
