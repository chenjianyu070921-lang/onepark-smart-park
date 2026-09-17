package ws

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zeromicro/go-zero/core/logx"
)

const (
	// sendBufferSize 每连接发送缓冲条数: 广播是尽力而为的,
	// 单个慢消费者不该拖慢其余连接(docs/m3/09 §2).
	sendBufferSize = 256
	// writeTimeout 单条消息写超时.
	writeTimeout = 10 * time.Second
	// readLimit 客户端上行报文上限(4KB): 告警通道只推不收, 超限视为滥用.
	readLimit = 4096
	// pingInterval 服务端心跳间隔.
	pingInterval = 30 * time.Second
	// pongWait 等待 pong 的读超时, 需大于 pingInterval.
	pongWait = 60 * time.Second
)

// conn 抽象 WebSocket 连接, 便于单测注入替身(真实实现即 *websocket.Conn).
type conn interface {
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	ReadMessage() (messageType int, p []byte, err error)
	Close() error
}

// Client 一条告警推送连接.
type Client struct {
	hub      *Hub
	conn     conn
	send     chan []byte
	tenantID int64 // 园区ID: 广播按租户过滤, 防止跨园区泄漏告警
}

// newClient 构造连接对象.
func newClient(hub *Hub, c conn, tenantID int64) *Client {
	return &Client{hub: hub, conn: c, send: make(chan []byte, sendBufferSize), tenantID: tenantID}
}

// readPump 只负责感知断开与回应 pong(告警通道不接收业务上行).
// 阻塞运行, 退出时自动注销连接.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(readLimit)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writePump 唯一拥有该连接写权限的 goroutine: 发送心跳 + 下发消息.
// 通道被关闭(Unregister)即退出.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.write(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			// 心跳失败说明连接已不可用, 直接退出由 readPump 兜底注销.
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				return
			}
		}
	}
}

// write 写一条消息(带写超时, 防止半开连接长期占用 goroutine).
func (c *Client) write(messageType int, payload []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return c.conn.WriteMessage(messageType, payload)
}

// kick 缓冲满(慢消费者)时强制下线.
func (c *Client) kick() {
	c.hub.Unregister(c)
	_ = c.conn.Close()
}

// Hub 连接注册表 + 广播器.
// relay 非空时进入跨实例模式: 投递改为"交给总线, 再由订阅回调本地投递"(docs/m3/09 §4).
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	relay   Relay
}

// NewHub 创建 Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

// Register 注册连接并启动读写两个泵.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	go c.writePump()
	go c.readPump()
}

// Unregister 注销连接并关闭其发送通道.
// 必须在写锁内同时完成"移出注册表"与"关闭通道": Broadcast 在读锁内向通道发送,
// 二者互斥才能保证不向已关闭的通道发送(否则 panic); 重复注销由 ok 判定兜底.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// Count 当前在线连接数.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// BroadcastTo 向指定园区的连接推送消息, 返回实际入队的连接数.
// tenantID <= 0 视为广播给全部连接(仅内部运维用途).
func (h *Hub) BroadcastTo(tenantID int64, msg []byte) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	delivered := 0
	for c := range h.clients {
		if tenantID > 0 && c.tenantID != tenantID {
			continue
		}
		select {
		case c.send <- msg:
			delivered++
		default:
			// 缓冲满 = 该连接已卡死(典型: 网络半开), 异步踢掉, 绝不阻塞其余连接.
			go c.kick()
		}
	}
	return delivered
}

// Push 推送一条告警事件给指定园区.
//
// 返回值语义随模式不同(两种模式下"未送达"都不是错误, 调用方不应据此报错):
//   - 单实例模式: 是否至少投给了一个在线连接; 没人看大屏时返回 false 属正常;
//   - 跨实例模式: 是否已交给总线 —— 投递是异步尽力而为的, 无法同步得知在线连接数.
func (h *Hub) Push(tenantID int64, envelope *Envelope) bool {
	if h == nil || envelope == nil {
		return false
	}
	payload, err := envelope.Encode()
	if err != nil {
		return false
	}

	r := h.relayOf()
	if r == nil {
		return h.BroadcastTo(tenantID, payload) > 0
	}

	// 跨实例: 交给总线统一分发, 本实例客户端由订阅回调投递(路径唯一, 不会重复推送).
	// 放到 goroutine 里执行 —— 广播是告警落库后的旁路, 不能因 Redis 抖动拖慢消费主流程(docs/m3/09 §5).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), relayPublishTimeout)
		defer cancel()
		if err := r.Publish(ctx, NewBusMessage(tenantID, payload)); err != nil {
			// 总线不可用降级为本地投递: 至少让本实例的大屏看得到, 不让告警静默消失.
			logx.Errorf("ws relay publish failed, fallback to local broadcast tenant=%d: %v", tenantID, err)
			h.BroadcastTo(tenantID, payload)
		}
	}()
	return true
}
