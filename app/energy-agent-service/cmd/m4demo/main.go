// m4demo 是 M4 模块的临时演示服务器, 答辩演示完可以整个删掉。
//
// 它干三件事:
//  1. 托管演示页面(静态文件), 避免 file:// 打开时浏览器拦跨域
//  2. 把 /api/energy /api/billing /api/agent 反代到真正的后端服务(8061~8064)
//  3. 补一个 /api/demo/billdetail —— 账单列表接口不返回计费明细,
//     明细存在 bill.detail 这个 JSON 列里, 直接从库里读出来给页面画峰谷图
//
// 用法: go run ./cmd/m4demo -dir <页面目录> -port 8070
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 各后端服务的地址
const (
	dataService     = "http://127.0.0.1:8061" // 52 实时 / 53 历史
	analysisService = "http://127.0.0.1:8062" // 56 日报 / 57 月报 / 58 区域详情
	billingService  = "http://127.0.0.1:8063" // 59~62 计费
	agentService    = "http://127.0.0.1:8064" // 智能体
	mysqlDSN        = "root:4ay1nkal3u8ed77y@tcp(115.191.16.159:3306)/onepark-smart-park?charset=utf8mb4&parseTime=true&loc=Local"
)

// 路径前缀 -> 后端服务
var routes = []struct {
	prefix string
	target string
}{
	{"/api/energy/daily", analysisService},
	{"/api/energy/monthly", analysisService},
	{"/api/energy/zone", analysisService},
	{"/api/energy/realtime", dataService},
	{"/api/energy/history", dataService},
	{"/api/billing/", billingService},
	{"/api/agent/", agentService},
}

func main() {
	dir := flag.String("dir", ".", "演示页面所在目录")
	port := flag.Int("port", 8070, "监听端口")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/demo/billdetail", handleBillDetail)
	mux.HandleFunc("/api/demo/health", handleHealth)
	mux.HandleFunc("/api/", proxy)
	mux.Handle("/", http.FileServer(http.Dir(*dir)))

	// 包一层跨域: 页面可能被别的端口(静态副本预览)托管, 那时它会用绝对地址直连这里,
	// 不同源, 没有跨域头浏览器会把响应拦掉
	handler := cors(mux)

	// 监听全部地址(含 IPv6 双栈): 只绑 127.0.0.1 时, 浏览器用 localhost 解析到 ::1 会连不上
	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("M4 演示面板: http://127.0.0.1:%d  (页面目录 %s)\n", *port, *dir)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// cors 允许任意来源访问。这是本地演示用的, 图省事开 *, 别照搬到生产
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleHealth 页面启动时探测后端通不通, 不通就在页面上明说, 免得对着空图表讲
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	type svc struct {
		Name string `json:"name"`
		Port int    `json:"port"`
		OK   bool   `json:"ok"`
	}
	list := []svc{
		{"数据服务 52/53", 8061, true},
		{"分析服务 56/57/58", 8062, true},
		{"计费服务 59~62", 8063, true},
		{"智能体服务", 8064, true},
	}
	cli := &http.Client{Timeout: 3 * time.Second}
	for i := range list {
		u := fmt.Sprintf("http://127.0.0.1:%d/api/energy/daily", list[i].Port)
		switch list[i].Port {
		case 8063:
			u = "http://127.0.0.1:8063/api/billing/rules"
		case 8064:
			u = "http://127.0.0.1:8064/api/agent/suggestions?page=1&pageSize=1"
		}
		resp, err := cli.Get(u)
		if err != nil {
			list[i].OK = false
			continue
		}
		resp.Body.Close()
		list[i].OK = resp.StatusCode < 500
	}
	writeJSON(w, map[string]interface{}{"code": "0", "data": list})
}

// handleBillDetail 读 bill.detail(JSON 列), 账单列表接口不返回它
// 峰谷账单的"每段用了多少度/多少钱"全在里面, 页面要用它画柱状图
func handleBillDetail(w http.ResponseWriter, r *http.Request) {
	billNo := r.URL.Query().Get("billNo")
	if billNo == "" {
		writeJSON(w, map[string]interface{}{"code": "1", "msg": "billNo 必填"})
		return
	}
	db, err := gorm.Open(mysql.Open(mysqlDSN), &gorm.Config{})
	if err != nil {
		writeJSON(w, map[string]interface{}{"code": "1", "msg": "数据库连接失败: " + err.Error()})
		return
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	var row struct {
		BillNo   string
		ZoneID   string
		Detail   string
		UsageKwh float64
		Amount   float64
	}
	err = db.Raw("SELECT bill_no, zone_id, IFNULL(detail,'') AS detail, usage_kwh AS usage_kwh, amount FROM bill WHERE bill_no = ?", billNo).
		Scan(&row).Error
	if err != nil {
		writeJSON(w, map[string]interface{}{"code": "1", "msg": err.Error()})
		return
	}
	if row.BillNo == "" {
		writeJSON(w, map[string]interface{}{"code": "1", "msg": "账单不存在: " + billNo})
		return
	}
	// detail 列里有些老账单存的是 Name/Usage/Price/Amount(大写),
	// 有些是新账单存的 name/usage/price/amount(小写)。统一转小写给页面用。
	var detail []map[string]interface{}
	if row.Detail != "" && row.Detail != "null" {
		var raw []map[string]interface{}
		if _ = json.Unmarshal([]byte(row.Detail), &raw); len(raw) > 0 {
			detail = make([]map[string]interface{}, 0, len(raw))
			for _, item := range raw {
				normalized := make(map[string]interface{}, len(item))
				for k, v := range item {
					normalized[strings.ToLower(k)] = v
				}
				detail = append(detail, normalized)
			}
		}
	}
	writeJSON(w, map[string]interface{}{
		"code": "0",
		"data": map[string]interface{}{
			"billNo": row.BillNo,
			"zoneId": row.ZoneID,
			"usage":  row.UsageKwh,
			"amount": row.Amount,
			"detail": detail,
		},
	})
}

// proxy 按前缀把请求转发到对应的后端服务
func proxy(w http.ResponseWriter, r *http.Request) {
	target := ""
	for _, rt := range routes {
		if strings.HasPrefix(r.URL.Path, rt.prefix) {
			target = rt.target
			break
		}
	}
	if target == "" {
		writeJSON(w, map[string]interface{}{"code": "1", "msg": "未知的演示接口: " + r.URL.Path})
		return
	}

	u, _ := url.Parse(target + r.URL.RequestURI())
	outReq := &http.Request{
		Method: r.Method,
		URL:    u,
		Header: make(http.Header),
	}
	for k, vs := range r.Header {
		if k == "Host" {
			continue
		}
		outReq.Header[k] = vs
	}
	if r.Body != nil {
		outReq.Body = r.Body
	}
	outReq.ContentLength = r.ContentLength

	// 智能体巡检要十几秒, 代理超时给宽一点
	cli := &http.Client{Timeout: 120 * time.Second}
	resp, err := cli.Do(outReq)
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"code": "1",
			"msg":  fmt.Sprintf("后端 %s 调不通: %v", target, err),
		})
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	b, _ := json.Marshal(v)
	_, _ = w.Write(b)
}
