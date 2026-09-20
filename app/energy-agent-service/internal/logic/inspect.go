package logic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"

	"onepark/app/energy-agent-service/internal/config"
	"onepark/app/energy-agent-service/internal/llm"
	"onepark/app/energy-agent-service/internal/model"
	"onepark/app/energy-agent-service/internal/rules"
	"onepark/app/energy-agent-service/internal/svc"
)

// InspectInput 一次巡检的入参。
// 统计日期由外部传入而不是在函数里取 time.Now(), 这样测试能指定任意一天,
// 也不会因为"跑的时间不同"得到不同结果 —— 确定性三板斧之一。
type InspectInput struct {
	ZoneID   string    // 只查某个区域, 空串表示全园区
	StatDate time.Time // 统计哪天的数据
	Trigger  string    // cron 定时 / manual 手动
	// DisableLLM 强制本次不调大模型, 走规则原文。
	// 演示或排查时用: 想对比"有大模型"和"没大模型"的差异, 就各跑一次
	DisableLLM bool
}

// InspectResult 巡检结果
type InspectResult struct {
	RunNo      string
	StatDate   string
	ZoneCount  int
	Findings   []rules.Finding
	LLMEnabled bool
	LLMModel   string
	CostMs     int
}

// RunInspect 跑一次完整巡检。手动调接口和定时任务是同一套逻辑,
// 保证"手动出的报告"和"自动出的报告"完全一致, 不会出现两套算法。
func RunInspect(ctx context.Context, svcCtx *svc.ServiceContext, in InspectInput) (*InspectResult, error) {
	start := time.Now()
	// 把配置里的阈值转成规则引擎用的结构。
	// 不直接在 rules 里引 config, 是为了让规则引擎保持"零依赖纯函数" —— 它好测就靠这个
	conf := svcCtx.Config.Thresholds()
	th := toRulesThreshold(conf)
	statDate := startOfDay(in.StatDate)

	run := model.AgentRun{
		RunNo:       fmt.Sprintf("R%s", start.Format("20060102-150405")),
		TriggerType: in.Trigger,
		Scope:       in.ZoneID,
		StatDate:    statDate.Format("2006-01-02"),
		Status:      model.RunStatusRunning,
		StartedAt:   start,
	}
	if err := svcCtx.Agent.CreateRun(ctx, &run); err != nil {
		return nil, fmt.Errorf("创建运行记录失败: %w", err)
	}

	res := &InspectResult{RunNo: run.RunNo, StatDate: run.StatDate}

	// ---- 1. 确定要扫哪些区域 ----
	zones, err := listZones(ctx, svcCtx, run.ID, in.ZoneID)
	if err != nil {
		finishRun(ctx, svcCtx, run.ID, model.RunStatusFailed, 0, 0, false, "", 0, err.Error())
		return nil, err
	}
	res.ZoneCount = len(zones)

	// ---- 2. 组装规则引擎的输入 ----
	// 基线期: 统计日往前推 N 天(不含统计日当天)。
	// BaselineDays 不属于"判定阈值", 它决定取多少天的数据, 所以留在配置层
	baseStart := statDate.AddDate(0, 0, -int(conf.BaselineDays))
	input := rules.Input{StatDate: statDate.Format("2006-01-02")}

	backwardMap, err := svcCtx.Reading.ListBackwardDevices(ctx, statDate)
	if err != nil {
		logx.Errorf("查读数回退设备失败: %v", err)
		backwardMap = map[string]bool{} // 查不到就当没有, 不阻断主流程
	}

	for _, zone := range zones {
		zi, err := buildZoneInput(ctx, svcCtx, run.ID, zone, baseStart, statDate, th)
		if err != nil {
			logx.Errorf("组装区域 %s 输入失败: %v", zone, err)
			continue // 一个区域失败不影响其他区域
		}
		input.Zones = append(input.Zones, zi)

		// 该区域下每台设备的历史与当天用量
		devices, err := svcCtx.Reading.ListDeviceDailyUsage(ctx, zone, statDate)
		if err != nil {
			logx.Errorf("查区域 %s 设备用量失败: %v", zone, err)
			continue
		}
		for _, d := range devices {
			hist, err := svcCtx.Reading.ListDeviceDailyUsageRange(ctx, d.DeviceID, baseStart, statDate)
			if err != nil {
				logx.Errorf("查设备 %s 历史失败: %v", d.DeviceID, err)
				hist = nil
			}
			input.Devices = append(input.Devices, rules.DeviceInput{
				DeviceID: d.DeviceID,
				ZoneID:   zone,
				History:  hist,
				Today:    d.Usage,
				HasData:  d.Usage > 0,
				Backward: backwardMap[d.DeviceID],
			})
		}
	}

	// ---- 3. 层1: 规则引擎判定(确定性, 与大模型无关) ----
	findings := rules.Analyze(input, th)

	// ---- 4. 层2: 大模型润色(失败自动回退到规则原文, 不影响结论) ----
	// run.ID 传进去, 大模型调用也归类到本次巡检的轨迹里,
	// 否则 agent_tool_call 里会堆一批 run_id=0 的孤儿记录
	findings = polish(ctx, svcCtx, run.ID, findings, run.StatDate, in.DisableLLM)

	// LLMModel 记的是实际用的实现: 真模型就显示模型名, 没启用或被本次关掉就显示 mock
	res.LLMModel = svcCtx.LLM.Name()
	if !in.DisableLLM && svcCtx.Config.LLM.Enable && svcCtx.Config.LLM.APIKey != "" {
		res.LLMEnabled = true
	}
	res.Findings = findings

	// ---- 5. 落库 ----
	sugs := make([]model.AgentSuggestion, 0, len(findings))
	for _, f := range findings {
		sugs = append(sugs, model.AgentSuggestion{
			RunID:      run.ID,
			StatDate:   statDate, // 参与去重: 同一天同类问题只留一条
			ZoneID:     f.ZoneID,
			DeviceID:   f.DeviceID,
			Category:   f.Category,
			Severity:   f.Severity,
			Title:      f.Title,
			Reason:     f.Reason,
			Evidence:   model.EncodeEvidence(f.Evidence),
			Confidence: f.Confidence,
			Action:     f.Action,
			Status:     model.SugStatusPending,
		})
	}
	if err := svcCtx.Agent.SaveSuggestions(ctx, sugs); err != nil {
		logx.Errorf("保存建议失败: %v", err)
	}

	res.CostMs = int(time.Since(start).Milliseconds())
	finishRun(ctx, svcCtx, run.ID, model.RunStatusSuccess,
		res.ZoneCount, len(findings), res.LLMEnabled, res.LLMModel, res.CostMs, "")
	return res, nil
}

// listZones 确定扫描范围, 顺便记一条工具调用
func listZones(ctx context.Context, svcCtx *svc.ServiceContext, runID uint64, zoneID string) ([]string, error) {
	st := time.Now()
	zones, err := svcCtx.Reading.ListZones(ctx)
	recordToolCall(ctx, svcCtx, runID, "list_zones", zoneID, zones, st, err)
	if err != nil {
		return nil, fmt.Errorf("查询区域列表失败: %w", err)
	}
	if zoneID == "" {
		return zones, nil
	}
	// 指定了区域就只扫它, 但要先确认这个区域真的存在
	for _, z := range zones {
		if z == zoneID {
			return []string{zoneID}, nil
		}
	}
	return nil, fmt.Errorf("区域 %s 没有任何能耗数据", zoneID)
}

// buildZoneInput 组装一个区域的分析输入
func buildZoneInput(ctx context.Context, svcCtx *svc.ServiceContext, runID uint64,
	zone string, baseStart, statDate time.Time, th rules.Threshold) (rules.ZoneInput, error) {

	dayEnd := statDate.AddDate(0, 0, 1)

	// 历史日用量(基线用, 不含统计当天)
	st := time.Now()
	hist, err := svcCtx.Reading.ListZoneDailyUsage(ctx, zone, baseStart, statDate)
	recordToolCall(ctx, svcCtx, runID, "query_zone_daily", zone, hist, st, err)
	if err != nil {
		return rules.ZoneInput{}, err
	}

	// 当天总用量
	todayList, err := svcCtx.Reading.ListZoneDailyUsage(ctx, zone, statDate, dayEnd)
	if err != nil {
		return rules.ZoneInput{}, err
	}
	var today float64
	for _, d := range todayList {
		today = d.Usage
	}

	// 当天分时(夜间空转判定要用)
	st = time.Now()
	hours, err := svcCtx.Reading.ListZoneHourlyUsage(ctx, zone, statDate)
	recordToolCall(ctx, svcCtx, runID, "query_zone_hourly", zone, hours, st, err)
	if err != nil {
		hours = nil
	}

	return rules.ZoneInput{
		ZoneID:    zone,
		History:   hist,
		Today:     today,
		HourToday: hours,
		HasData:   today > 0,
	}, nil
}

// toExplainRequest 把规则的证据翻译成给大模型的字段。
//
// 不能直接 f.Evidence["actual"] 一把梭: 各类异常存进证据的键名不一样,
// 夜间空转存的是 night/total/share, 压根没有 actual 这个键。
// 之前就是在这一步取到 0, 结果发给大模型的提示词写着
// "当天用量: 0.00 度, 偏离幅度: 90%" —— 模型拿到 0 度却要说"用得太多",
// 只能瞎编或者干脆不提数字。修法: 没有 actual 就用 total 顶上。
func toExplainRequest(f *rules.Finding, statDate string) llm.ExplainRequest {
	req := llm.ExplainRequest{
		ZoneID:    f.ZoneID,
		DeviceID:  f.DeviceID,
		Category:  f.Category,
		StatDate:  statDate,
		Usage:     f.Evidence["actual"],
		Baseline:  f.Evidence["baseline"],
		Deviation: f.Evidence["deviation"],
	}
	if req.Usage == 0 {
		req.Usage = f.Evidence["total"]
	}
	return req
}

// polish 用大模型给每条发现润色解释。
// 任何一条失败都只影响那一条的表述, 用规则原文顶上, 不会让整次巡检失败。
func polish(ctx context.Context, svcCtx *svc.ServiceContext, runID uint64,
	list []rules.Finding, statDate string, disable bool) []rules.Finding {
	// 本次强制关掉大模型时换成 mock, 这样能对比"有/无大模型"的效果差异
	client := svcCtx.LLM
	if disable {
		client = &llm.MockClient{}
	}
	if len(list) == 0 {
		return list
	}
	for i := range list {
		f := &list[i]
		req := toExplainRequest(f, statDate)
		st := time.Now()
		// 兜底文本就是规则自己写的解释, 大模型不可用时原样返回
		text, _, err := llm.ExplainWithFallback(ctx, client, req, f.Reason)
		if err != nil {
			logx.Errorf("大模型解释失败(已用规则原文兜底): %v", err)
		}
		if text != "" {
			f.Reason = text
		}
		recordToolCall(ctx, svcCtx, runID, "llm_explain", f.Category, text, st, err)
	}
	return list
}

// recordToolCall 记一条工具调用轨迹。失败也记录, 这样能统计工具成功率
func recordToolCall(ctx context.Context, svcCtx *svc.ServiceContext, runID uint64,
	tool, param string, result interface{}, st time.Time, err error) {

	ok := 1
	errMsg := ""
	if err != nil {
		ok = 0
		errMsg = err.Error()
	}
	// 结果只存摘要, 全量存进来表会涨得很快
	sum := fmt.Sprintf("%v", result)
	if len(sum) > 200 {
		sum = sum[:200] + "..."
	}
	call := model.AgentToolCall{
		RunID:      runID,
		ToolName:   tool,
		Params:     fmt.Sprintf(`{"param":"%s"}`, strings.ReplaceAll(param, `"`, "")),
		ResultSum:  sum,
		DurationMs: int(time.Since(st).Milliseconds()),
		Success:    ok,
		ErrorMsg:   truncate(errMsg, 250),
	}
	if saveErr := svcCtx.Agent.AddToolCall(ctx, &call); saveErr != nil {
		logx.Errorf("记录工具调用失败: %v", saveErr)
	}
}

// finishRun 更新运行记录为终态。
// zoneCount / findCount 各归各位: 之前把区域数当成发现数写进 find_count,
// 而 zone_count 压根没写, 结果表里每条都是 "扫了0个区域, 发现9条异常",
// 拿这个数据算"每次巡检平均扫多少区域"就全是错的。
func finishRun(ctx context.Context, svcCtx *svc.ServiceContext, runID uint64,
	status, zoneCount, findCount int, llmEnabled bool, llmModel string, costMs int, errMsg string) {

	if e := svcCtx.Agent.FinishRun(ctx, runID, status, zoneCount, findCount,
		llmEnabled, llmModel, costMs, truncate(errMsg, 500)); e != nil {
		logx.Errorf("更新运行记录失败: %v", e)
	}
}

// toRulesThreshold 把配置项转成规则引擎的阈值结构
func toRulesThreshold(t config.ThresholdConf) rules.Threshold {
	return rules.Threshold{
		SpikeRatio:    t.SpikeRatio,
		ZoneRatio:     t.ZoneRatio,
		NightRatio:    t.NightRatio,
		MinUsage:      t.MinUsage,
		MinConfidence: t.MinConfidence,
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ErrSuggestionNotFound 建议不存在
var ErrSuggestionNotFound = gorm.ErrRecordNotFound
