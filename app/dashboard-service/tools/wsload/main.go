// wsload 大屏 WebSocket 推送压测工具 —— 量「N 个客户端同时在线」时的推送表现。
//
// 为什么要单独测 WS: 大屏的实时性承诺是"5 秒一帧快照 + 事件即时推送",
// HTTP 压测(见 tools/dashload)完全测不到这条路径 —— 它走的是长连接 + 广播 + 写泵,
// 瓶颈可能在完全不同的地方(每个客户端一个 goroutine + 一个有缓冲 channel)。
//
// 它同时回答计划书加练 B 的两个问题:
//  1. N 个客户端的推送间隔稳不稳(应为 5s ± 抖动), 有没有客户端掉队
//  2. **客户端断开后服务端有没有及时清理** —— 连接泄漏会表现为"在线数只增不减",
//     所以本工具跑完会提示去看服务端日志里的 online= 计数(那边才是权威口径)
//
// 用法:
//
//	go run ./app/dashboard-service/tools/wsload -n 200 -d 30s -jwt-secret xxx
//
// 说明:
//
//	-jwt-secret 与 dashboard 服务的 JWT_SECRET 必须一致(WS 鉴权走 ?token=)。
//	-ramp 控制建连速度, 默认每 10ms 建一个 —— 瞬间建 200 个连接容易被误判为"服务端扛不住",
//	而那其实是压测端在建连风暴, 与实际使用(客户端逐个上线)不符。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"onepark/common/jwt"
)

// pushMsg 只解析我们关心的字段 —— 大屏推送体里有 type 与 data。
type pushMsg struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// snapshotData 快照里的时间戳(epoch 秒), 用于估算推送延迟。
type snapshotData struct {
	UpdatedAt int64 `json:"updated_at"`
	ElapsedMs int64 `json:"elapsed_ms"`
}

func main() {
	wsURL := flag.String("url", "ws://127.0.0.1:18052/ws/dashboard", "WS 地址(不含 token)")
	secret := flag.String("jwt-secret", "", "JWT 密钥(须与服务端 JWT_SECRET 一致)")
	token := flag.String("token", "", "直接给定 token(与 -jwt-secret 二选一)")
	tenant := flag.Int64("tenant", 1, "租户 id(写进 JWT)")
	n := flag.Int("n", 100, "客户端数量")
	dur := flag.Duration("d", 30*time.Second, "在线时长")
	ramp := flag.Duration("ramp", 10*time.Millisecond, "建连间隔")
	flag.Parse()

	if *n <= 0 {
		fmt.Fprintln(os.Stderr, "[wsload] -n 必须为正数")
		os.Exit(1)
	}
	tk := *token
	if tk == "" {
		if *secret == "" {
			fmt.Fprintln(os.Stderr, "[wsload] 必须提供 -jwt-secret 或 -token")
			os.Exit(1)
		}
		var err error
		// access 类型的令牌才允许接入大屏(refresh 会被拒)
		tk, err = jwt.Generate(*secret, 1, "1", *tenant, "access", int64((*dur+time.Minute).Seconds()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wsload] 生成 token 失败: %v\n", err)
			os.Exit(1)
		}
	}

	type clientStat struct {
		connOK    bool
		msgs      int
		pushGaps  []time.Duration // 相邻两次推送的间隔
		latencies []time.Duration // 估算延迟(秒级粒度, 见下)
		firstErr  string
		readErrs  int
	}

	stats := make([]clientStat, *n)
	wantMsgs := int(*dur / (5 * time.Second))

	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			u, _ := url.Parse(*wsURL)
			q := u.Query()
			q.Set("token", tk)
			u.RawQuery = q.Encode()

			dl := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
			conn, _, err := dl.Dial(u.String(), nil)
			if err != nil {
				stats[idx].firstErr = err.Error()
				return
			}
			stats[idx].connOK = true
			defer conn.Close()

			deadline := time.Now().Add(*dur)
			_ = conn.SetReadDeadline(deadline)
			var last time.Time
			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					stats[idx].readErrs++
					break
				}
				now := time.Now()
				stats[idx].msgs++
				if !last.IsZero() {
					stats[idx].pushGaps = append(stats[idx].pushGaps, now.Sub(last))
				}
				last = now

				var m pushMsg
				if json.Unmarshal(payload, &m) == nil && m.Type == "snapshot" {
					var sd snapshotData
					if json.Unmarshal(m.Data, &sd) == nil && sd.UpdatedAt > 0 {
						// ⚠️ updated_at 是**秒级**: 这个延迟只能当数量级参考, 不是精确值
						stats[idx].latencies = append(stats[idx].latencies,
							now.Sub(time.Unix(sd.UpdatedAt, 0)))
					}
				}
			}
		}(i)
		time.Sleep(*ramp)
	}
	wg.Wait()

	// ---- 汇总 ----
	okCnt, failCnt, msgTotal, readErrCnt := 0, 0, 0, 0
	var allGaps, allLat []time.Duration
	var firstErrs []string
	for _, s := range stats {
		if s.connOK {
			okCnt++
		} else {
			failCnt++
			if len(firstErrs) < 3 && s.firstErr != "" {
				firstErrs = append(firstErrs, s.firstErr)
			}
		}
		msgTotal += s.msgs
		readErrCnt += s.readErrs
		allGaps = append(allGaps, s.pushGaps...)
		allLat = append(allLat, s.latencies...)
	}
	pct := func(ds []time.Duration, p float64) time.Duration {
		if len(ds) == 0 {
			return 0
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[int(float64(len(ds)-1)*p)]
	}

	fmt.Printf("=== wsload 结果 ===\n")
	fmt.Printf("目标            %s\n", *wsURL)
	fmt.Printf("客户端/时长     %d 个 / %s   (服务端快照周期 5s, 期望每客户端约 %d 条)\n", *n, *dur, wantMsgs)
	fmt.Printf("建连            成功 %d, 失败 %d\n", okCnt, failCnt)
	for _, e := range firstErrs {
		fmt.Printf("                失败样例: %s\n", e)
	}
	fmt.Printf("收到消息        总计 %d 条, 平均每客户端 %.1f 条\n",
		msgTotal, float64(msgTotal)/float64(max(okCnt, 1)))
	fmt.Printf("推送间隔        p50=%s  p95=%s  max=%s   (期望 5s)\n",
		pct(allGaps, 0.50).Round(time.Millisecond),
		pct(allGaps, 0.95).Round(time.Millisecond),
		pct(allGaps, 0.99).Round(time.Millisecond))
	fmt.Printf("估算延迟        p50=%s  p95=%s   (⚠️ 由秒级 updated_at 估算, 仅看数量级)\n",
		pct(allLat, 0.50).Round(time.Millisecond), pct(allLat, 0.95).Round(time.Millisecond))
	fmt.Printf("读结束原因      正常收尾%d次(读到时长上限), 异常断读%d次 —— 二者相减才是中途掉线\n",
		okCnt, readErrCnt-okCnt)

	// 掉线排查: 连接成功但收不到消息的客户端
	silent := 0
	for _, s := range stats {
		if s.connOK && s.msgs < wantMsgs-1 {
			silent++
		}
	}
	if silent > 0 {
		fmt.Printf("⚠️  %d 个客户端收到的消息明显少于期望(可能中途掉线或被踢)\n", silent)
	}
	fmt.Printf("\n下一步(连接泄漏检查): 客户端已全部断开, 请看服务端日志末尾的 online= 计数\n")
	fmt.Printf("  应回落到 0; 若只增不减即为泄漏。日志关键行: [ws] 客户端接入/断开 ... online=N\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
