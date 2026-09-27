package wshub

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket 推送压测（计划书 Day 4 加练 B）。
//
// 为什么放在测试里而不是外部压测工具: 这一层要量的是「广播到 N 个在线连接」的成本,
// 它跟 HTTP 无关也跟网关无关 —— 进程内起真实 WS 连接(httptest + 真升级)就能量准,
// 而且**可复跑、无需手工起服务**（外部工具换个环境就容易变成"跑不起来"）。
//
// 覆盖四件事:
//  1. 推送延迟: 从 Broadcast 到**全部** N 个客户端收到, 分位数
//  2. 每客户端内存: 连接建立后的堆增量 / N
//  3. 断开清理: 客户端全断开后 Count 是否回落到 0(**连接泄漏**的核心检查)与耗时
//  4. 缓冲积压: 每连接 send 缓冲只有 16 条(sendBufferSize), 连续广播能否全部收到

// loadPayload 模拟一帧大屏快照的体量。真实快照(四张卡片 + degraded + 时间戳)
// 约 0.5~1KB; 这里取 1KiB, 体量变化时成本基本线性。
func loadPayload(size int) []byte {
	m := map[string]any{"type": "snapshot", "data": map[string]any{
		"pad": string(make([]byte, size)),
	}}
	b, _ := json.Marshal(m)
	return b
}

func heapAlloc() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestWSLoad_BroadcastAndCleanup 三档客户端数(50/200/500)各跑一轮: 推送延迟 + 内存 + 断开清理。
func TestWSLoad_BroadcastAndCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("压测用例, -short 下跳过")
	}
	const (
		rounds  = 5
		payload = 1024
	)
	msg := loadPayload(payload)

	for _, n := range []int{50, 200, 500} {
		t.Run(fmt.Sprintf("clients=%d", n), func(t *testing.T) {
			hub := NewHub()
			wsURL := startTestWS(t, hub)

			heapBefore := heapAlloc()

			conns := make([]*websocket.Conn, 0, n)
			for i := 0; i < n; i++ {
				d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
				conn, _, err := d.Dial(wsURL, nil)
				if err != nil {
					t.Fatalf("第 %d 个客户端连接失败: %v", i, err)
				}
				conns = append(conns, conn)
			}
			waitFor(t, func() bool { return hub.Count() == n })
			heapWithClients := heapAlloc()

			// 每个客户端一个读协程, 每轮用一个 WaitGroup 等"全部收到"
			var received atomic.Int64
			done := make(chan struct{})
			var readers sync.WaitGroup
			for _, c := range conns {
				readers.Add(1)
				go func(c *websocket.Conn) {
					defer readers.Done()
					for {
						select {
						case <-done:
							return
						default:
						}
						_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
						if _, _, err := c.ReadMessage(); err != nil {
							return
						}
						received.Add(1)
					}
				}(c)
			}

			var (
				latencies []time.Duration
				gotTotal  int64
			)
			for r := 0; r < rounds; r++ {
				received.Store(0)
				start := time.Now()
				hub.Broadcast(msg)
				// 等到全部客户端都收到这一帧
				deadline := time.Now().Add(3 * time.Second)
				for received.Load() < int64(n) && time.Now().Before(deadline) {
					time.Sleep(200 * time.Microsecond)
				}
				latencies = append(latencies, time.Since(start))
				gotTotal += received.Load()
			}
			close(done)
			readers.Wait()

			sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
			pick := func(p float64) time.Duration { return latencies[int(float64(len(latencies)-1)*p)] }
			perClientKB := float64(heapWithClients-heapBefore) / float64(n) / 1024

			t.Logf("客户端=%d  推送延迟 p50=%s  p95=%s  max=%s  实收 %d/%d 条",
				n, pick(0.5).Round(time.Microsecond), pick(0.95).Round(time.Microsecond),
				latencies[len(latencies)-1].Round(time.Microsecond), gotTotal, int64(n*rounds))
			t.Logf("客户端=%d  每连接堆占用≈%.1f KB (含读/写泵 goroutine)", n, perClientKB)

			if gotTotal < int64(n*rounds) {
				t.Errorf("有帧没被全部收到: 实收 %d, 期望 %d —— 说明广播会丢消息(发送缓冲只有 %d 条)",
					gotTotal, n*rounds, sendBufferSize)
			}

			// ---- 断开清理: 连接泄漏的核心检查 ----
			closeStart := time.Now()
			for _, c := range conns {
				_ = c.Close()
			}
			waitFor(t, func() bool { return hub.Count() == 0 })
			cleanup := time.Since(closeStart)
			heapAfter := heapAlloc()
			t.Logf("客户端=%d  全部断开后 Count=0, 清理耗时=%s, 堆回落至 %.1f MB (建连前 %.1f MB)",
				n, cleanup.Round(time.Millisecond), float64(heapAfter)/1024/1024, float64(heapBefore)/1024/1024)

			if cleanup > 3*time.Second {
				t.Errorf("清理耗时 %s 偏长, 疑似不及时", cleanup)
			}
		})
	}
}

// TestWSLoad_SlowClientDoesNotBlockOthers 一个读得慢的客户端不应拖住整场广播。
//
// 这是长连接服务最容易出的问题: 某个客户端网络卡住 -> 写泵阻塞 -> 若广播是同步写 socket,
// **所有人一起卡**。本项目的设计是每连接一个带缓冲的 channel + 写泵异步落盘, 这里验证它真的成立。
func TestWSLoad_SlowClientDoesNotBlockOthers(t *testing.T) {
	if testing.Short() {
		t.Skip("压测用例, -short 下跳过")
	}
	const healthy = 20
	hub := NewHub()
	wsURL := startTestWS(t, hub)

	// 慢客户端: 连上后**不读**
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	slow, _, err := d.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("慢客户端连接失败: %v", err)
	}
	defer slow.Close()

	conns := make([]*websocket.Conn, 0, healthy)
	for i := 0; i < healthy; i++ {
		c, _, err := d.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("正常客户端连接失败: %v", err)
		}
		conns = append(conns, c)
		defer c.Close()
	}
	waitFor(t, func() bool { return hub.Count() == healthy+1 })

	var got atomic.Int64
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
				got.Add(1)
			}
		}(c)
	}

	msg := loadPayload(1024)
	start := time.Now()
	for i := 0; i < 5; i++ {
		hub.Broadcast(msg)
	}
	deadline := time.Now().Add(3 * time.Second)
	for got.Load() < int64(healthy*5) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Microsecond)
	}
	elapsed := time.Since(start)
	close(done)
	wg.Wait()

	t.Logf("慢客户端(不读)在场时, %d 个正常客户端收到 %d/%d 条, 耗时 %s",
		healthy, got.Load(), healthy*5, elapsed.Round(time.Millisecond))
	if got.Load() < int64(healthy*5) {
		t.Errorf("正常客户端被慢客户端拖住: 只收到 %d/%d 条", got.Load(), healthy*5)
	}
}

// TestWSLoad_ConnectionCountersNoLeak 反复"连上-断开"多轮, 在线数必须回到 0(而不是越攒越多)。
//
// 单次断开能清理不代表多轮不泄漏 —— goroutine / map 条目泄漏往往在反复连断后才显形。
func TestWSLoad_ConnectionCountersNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("压测用例, -short 下跳过")
	}
	hub := NewHub()
	wsURL := startTestWS(t, hub)
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	for round := 0; round < 3; round++ {
		var conns []*websocket.Conn
		for i := 0; i < 100; i++ {
			c, _, err := d.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("第 %d 轮第 %d 个连接失败: %v", round, i, err)
			}
			conns = append(conns, c)
		}
		waitFor(t, func() bool { return hub.Count() == 100 })
		for _, c := range conns {
			_ = c.Close()
		}
		waitFor(t, func() bool { return hub.Count() == 0 })
		t.Logf("第 %d 轮: 100 连接建立后又全部断开, 在线数已回到 0", round+1)
	}
}
