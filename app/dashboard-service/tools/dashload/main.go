// dashload 大屏接口并发压测工具 —— 给 `/api/dashboard/overview` 出真实的分位数。
//
// 为什么需要它: benchmark 量的是**聚合层净开销**(桩替代上游), 而答辩/演示被问的是
// 「这个接口到底扛不扛得住」—— 那就得对**真实服务**发真实 HTTP 请求, 拿真实分位数。
//
// 它同时统计**缓存命中率**(读响应体的 `cached` 字段)与**超预算请求数**,
// 因为这两个数比"平均值"有意义得多:
//   - 平均 20ms 但 p99 是 3s 的接口, 在演示现场就是"卡住了";
//   - 命中率 0% 说明缓存没生效, 再快的单次也扛不住并发。
//
// 用法:
//
//	go run ./app/dashboard-service/tools/dashload -url http://127.0.0.1:18052/api/dashboard/overview -c 100 -d 10s
//	go run ./app/dashboard-service/tools/dashload -c 500 -d 10s -budget 800ms
//
// 参数:
//
//	-c       并发数(默认 100)
//	-d       压测时长(默认 10s)
//	-budget  延迟预算(默认 800ms, 对应接口的整体超时承诺), 超出计入 "超预算"
//	-tenant  请求头 x-tenant-id(默认 0; 生产由网关注入)
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// classifyErr 把网络错误归一成可读类别 —— 报告里要写"什么原因", 不是"失败了"。
func classifyErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "actively refused") || strings.Contains(s, "connection refused"):
		return "连接被拒(服务未监听/端口耗尽)"
	case strings.Contains(s, "reset by peer") || strings.Contains(s, "forcibly closed"):
		return "连接被重置(服务端主动断开/队列满)"
	case strings.Contains(s, "Timeout") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "超时"
	case strings.Contains(s, "EOF"):
		return "连接被提前关闭(EOF)"
	default:
		return "其他: " + s
	}
}

func main() {
	url := flag.String("url", "http://127.0.0.1:18052/api/dashboard/overview", "目标 URL")
	conc := flag.Int("c", 100, "并发数")
	dur := flag.Duration("d", 10*time.Second, "压测时长")
	budget := flag.Duration("budget", 800*time.Millisecond, "延迟预算, 超出计入超预算")
	tenant := flag.Int64("tenant", 0, "请求头 x-tenant-id")
	rotate := flag.Bool("rotate-tenant", false,
		"每个请求换一个租户 -> 缓存必然不命中, 用于测**穿透路径**\n"+
			"(不加则是稳态命中路径: 同一租户在 30s 缓存内几乎全部命中)")
	flag.Parse()

	if *conc <= 0 {
		fmt.Fprintln(os.Stderr, "[dashload] -c 必须为正数")
		os.Exit(1)
	}

	// 自建 Transport: 压测要放大并发连接数, 且**不复用**默认客户端的空闲连接上限。
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *conc * 2,
			MaxIdleConnsPerHost: *conc * 2,
			MaxConnsPerHost:     0,
		},
	}

	var (
		okCnt, failCnt atomic.Int64
		cachedCnt      atomic.Int64
		overBudget     atomic.Int64
		statusNon200   atomic.Int64
		mu             sync.Mutex
		allLatency     []time.Duration    // 单机几百万以内, 直接收集后排序求分位数(精确值胜过近似)
		errKinds       = map[string]int{} // 失败原因归类
	)

	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			local := make([]time.Duration, 0, 256)
			seq := 0
			for time.Now().Before(deadline) {
				req, err := http.NewRequest(http.MethodGet, *url, nil)
				if err != nil {
					failCnt.Add(1)
					continue
				}
				tid := *tenant
				if *rotate {
					// 每个请求都是新租户 -> 缓存 key 全新, 必然穿透到聚合并写一次缓存
					seq++
					tid = int64(worker)*1_000_000 + int64(seq)
				}
				req.Header.Set("x-tenant-id", fmt.Sprintf("%d", tid))

				start := time.Now()
				resp, err := client.Do(req)
				cost := time.Since(start)
				if err != nil {
					failCnt.Add(1)
					// 失败原因必须归类: 只报"失败 17528 次"等于没说 ——
					// 是连不上、被重置、还是超时, 对应完全不同的处置。
					mu.Lock()
					errKinds[classifyErr(err)]++
					mu.Unlock()
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					statusNon200.Add(1)
				} else {
					okCnt.Add(1)
				}
				if strings.Contains(string(body), `"cached":true`) {
					cachedCnt.Add(1)
				}
				if cost > *budget {
					overBudget.Add(1)
				}
				local = append(local, cost)
			}
			mu.Lock()
			allLatency = append(allLatency, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	total := len(allLatency)
	if total == 0 {
		fmt.Fprintln(os.Stderr, "[dashload] 一个请求都没成功发出, 请检查 URL 与服务是否在跑")
		os.Exit(1)
	}
	sort.Slice(allLatency, func(i, j int) bool { return allLatency[i] < allLatency[j] })
	pct := func(p float64) time.Duration {
		idx := int(float64(total-1) * p)
		if idx < 0 {
			idx = 0
		}
		return allLatency[idx]
	}

	var sum time.Duration
	for _, d := range allLatency {
		sum += d
	}
	hitRate := float64(0)
	if ok := okCnt.Load(); ok > 0 {
		hitRate = float64(cachedCnt.Load()) / float64(ok) * 100
	}

	fmt.Printf("=== dashload 结果 ===\n")
	fmt.Printf("目标      %s\n", *url)
	fmt.Printf("并发/时长 %d 路 / %s\n", *conc, *dur)
	fmt.Printf("总请求    %d   (200: %d, 非200: %d, 失败: %d)\n",
		total, okCnt.Load(), statusNon200.Load(), failCnt.Load())
	fmt.Printf("QPS       %.0f\n", float64(total)/dur.Seconds())
	fmt.Printf("延迟      p50=%s  p95=%s  p99=%s  max=%s  avg=%s\n",
		pct(0.50), pct(0.95), pct(0.99), allLatency[total-1], (sum / time.Duration(total)).Round(time.Microsecond))
	fmt.Printf("缓存命中  %.1f%%   (%d/%d)\n", hitRate, cachedCnt.Load(), okCnt.Load())
	fmt.Printf("超预算    %d 次 (>%s, 占 %.3f%%)\n", overBudget.Load(), *budget,
		float64(overBudget.Load())/float64(total)*100)
	if len(errKinds) > 0 {
		fmt.Printf("失败原因  ")
		keys := make([]string, 0, len(errKinds))
		for k := range errKinds {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				fmt.Printf("          ")
			}
			fmt.Printf("%s: %d 次\n", k, errKinds[k])
		}
	}

	// 预算守不住时**明确非零退出**: 脚本里可以直接当断言用, 不必人眼比对
	if overBudget.Load() > 0 {
		fmt.Fprintf(os.Stderr, "[dashload] 有请求超出预算 %s —— 演示现场会表现为「卡住」\n", *budget)
		os.Exit(1)
	}
}
