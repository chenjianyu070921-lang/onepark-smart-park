// Package wsserver 大屏 WebSocket 推送服务(清单 #73).
//
// 当前采用「周期快照」策略: 每 5s 复用 Overview 聚合逻辑构建快照并广播。
// 之所以先做快照而不是纯事件驱动:
//   - 事件源(共享 Kafka topic)尚未获授权接入, 快照不依赖任何外部条件, 现在就能用;
//   - Overview 自带 30s 租户缓存, 5s 推送基本都命中缓存, 成本极低;
//   - 大屏的"实时"诉求是秒级, 5s 快照已满足; Kafka 事件驱动留待授权后叠加,
//     到时只需在本包加一个事件入口调 hub.Broadcast, 推送通道不变。
//
// 鉴权: 按既定决策走 `?token=`(浏览器 WebSocket 无法自定义 Authorization 头)。
// 校验规则: JWT 签名有效、未过期、且为 access 类型(refresh 令牌不允许接入大屏)。
// jwtSecret 必须来自环境变量, 为空时拒绝所有连接(服务配置错误, 宁可不服务不裸奔)。
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
	"onepark/common/jwt"
)

// pushInterval 快照推送间隔.
const pushInterval = 5 * time.Second

// snapshotMsg 推送给大屏的消息结构.
type snapshotMsg struct {
	Type string              `json:"type"`
	Data *types.OverviewResp `json:"data"`
}

// Handler 返回 WebSocket 升级处理器, 路由为 GET /ws/dashboard?token=xxx.
func Handler(hub *wshub.Hub, jwtSecret string) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// 大屏前端与服务可能不同源; 开发期放开, 上线前必须收敛为域名白名单
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// M6 网关已就绪: 必须校验 token, 否则任何人可连 WS 获取大屏聚合数据.
		if jwtSecret == "" {
			logx.WithContext(r.Context()).Errorf("[ws] jwt secret not configured, refuse ws connection")
			http.Error(w, "server misconfigured", http.StatusInternalServerError)
			return
		}
		tokenStr := r.URL.Query().Get("token")
		if tokenStr == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		claims, err := jwt.Parse(jwtSecret, tokenStr)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if claims.Type != jwt.TypeAccess {
			http.Error(w, "refresh token not allowed", http.StatusUnauthorized)
			return
		}

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
func StartSnapshotPush(ctx context.Context, svcCtx *svc.ServiceContext, hub *wshub.Hub) {
	logger := logx.WithContext(ctx)
	ticker := time.NewTicker(pushInterval)
	defer ticker.Stop()

	logger.Infof("[ws] 快照推送已启动: interval=%s", pushInterval)
	for {
		select {
		case <-ctx.Done():
			logger.Info("[ws] 快照推送已停止")
			return
		case <-ticker.C:
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
