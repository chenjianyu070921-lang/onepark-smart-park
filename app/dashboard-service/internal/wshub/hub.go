// Package wshub 提供大屏 WebSocket 连接管理与广播.
//
// 核心约束: gorilla/websocket **同一连接不允许并发写**, 并发写会直接 panic。
// 因此每个连接独享一个 writer goroutine + 带缓冲的发送通道:
// 广播方只往通道塞消息, 真正写 socket 的永远只有 writer 这一个 goroutine。
//
// 多实例扇出说明: 当前单实例内存广播。将来多实例部署时,
// 广播入口需换成 Redis Pub/Sub 扇出(已记为技术债), 本包接口不变。
package wshub

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// sendBufferSize 每连接发送缓冲条数。
	// 广播是"尽力而为"的: 某块大屏卡顿不应该拖慢其余大屏的推送。
	sendBufferSize = 16
	// writeTimeout 单条消息写超时.
	writeTimeout = 5 * time.Second
	// readLimit 客户端上行消息上限 —— 大屏只收不发, 收到超限报文视为异常.
	readLimit = 512
)

// Client 一条大屏连接.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// NewClient 创建连接对象.
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, sendBufferSize),
	}
}

// ReadPump 只负责感知断开(大屏不往上发业务数据).
// 收到错误或连接关闭时自动注销; 阻塞, 应在独立 goroutine 中运行.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(readLimit)
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// WritePump 唯一拥有该连接写权限的 goroutine.
// 从发送通道取消息写 socket; 通道被关闭(Unregister)后退出.
func (c *Client) WritePump() {
	defer func() { _ = c.conn.Close() }()
	for msg := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// Hub 连接注册表 + 广播器.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

// NewHub 创建 Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

// Register 注册连接.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// Unregister 注销连接并关闭其发送通道.
//
// 必须在写锁内同时完成"移出注册表"与"关闭通道":
// Broadcast 在读锁内向通道发送, 二者互斥才能保证不向已关闭的通道发送(否则 panic)。
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

// Broadcast 向所有在线连接推送消息.
//
// 单连接发送缓冲满 -> 该连接消费端已卡死(典型: 网络半开),
// 异步踢掉它, 绝不阻塞广播拖慢其余大屏。
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			go c.kick()
		}
	}
}

// kick 缓冲满时强制下线.
func (c *Client) kick() {
	c.hub.Unregister(c)
	_ = c.conn.Close()
}
