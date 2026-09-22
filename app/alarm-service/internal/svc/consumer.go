package svc

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/search"
	"onepark/app/alarm-service/internal/ws"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/zeromicro/go-zero/core/logx"
)

// M1 设备上报的设备类型 / 事件类型取值 (DeviceEvent v1, 见 docs/m3/10).
const (
	DeviceTypeAccessControl = "access_control" // 门禁设备
	EventTypeIntrusion      = "intrusion"      // 非法闯入
)

// keyDedup L1 幂等键前缀, 完整键形如 alarm:dedup:{幂等ID}, TTL 24h.
const keyDedup = "alarm:dedup:"

// keyCooldown L2 业务冷却键前缀, 完整键形如
// alarm:cooldown:{deviceID}:{eventType}:{ruleID}, TTL 见 svc.cooldownTTL(docs/m3/08 §1).
const keyCooldown = "alarm:cooldown:"

// hardcodedRuleID 硬编码规则的规则ID: 0 表示"未走规则引擎"(与 alarm.rule_id 语义一致).
// 规则引擎上线后, 若库里存在启用规则则不再走该回退分支.
const hardcodedRuleID = 0

// ErrMalformedEvent 消息本身有问题(反序列化失败 / 必填字段缺失), 属于**不可重试**错误:
// 重试多少次结果都一样, 只能入死信等人工处理. 由 HandleWithDeadLetter 识别并直接落台账.
var ErrMalformedEvent = errors.New("alarm: malformed device event")

// 重试退避策略(docs/m3/06 §5.1): 3 次重试, 累计 2.6s,
// 远小于 Consumer.Group.Rebalance.Timeout(60s), 不会触发不必要的重平衡.
var retryBackoff = []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

// DeviceEvent M1 设备遥测事件, 按 common/kafka.DeviceTelemetry 统一契约(2026-09-18 定稿)解析:
// 事件时间取 occurred_at(Unix 秒), 区域取 zone_id(能源区域编码).
// AreaID/Timestamp 为契约定稿前的旧字段, 仅用于兼容历史消息, 新生产端不会发送.
// 字段缺失时的降级策略见 IdempotentID / MatchIntrusionRule 注释.
//
// Source 生产端来源(mqtt / tcp-gateway / http-fallback), 仅用于排查, 不参与判定.
//
// ⚠️ 入站兼容(2026-09-20 联调取证): 生产端已统一到 occurred_at, 但历史消息与
// device-service 的 HTTP 降级通道仍可能只带 timestamp(毫秒)。两个时间字段都必须认 ——
// 只认其中一个时, 另一类消息的事件时间恒为 0, 而 IdempotentID 的指纹降级把时间算进哈希,
// 恒为 0 会让"同一设备同一类型"的所有事件指纹相同, 被 L1 幂等判为已处理丢弃(静默丢告警).
// 详见 EventTime 的注释.
type DeviceEvent struct {
	RequestID string `json:"request_id"`
	TenantID  int64  `json:"tenant_id"`
	DeviceID  string `json:"device_id"`
	// DeviceType 设备类型; M1 侧为 optional, 缺失时规则引擎按"不限设备类型"处理.
	DeviceType string          `json:"device_type"`
	EventType  string          `json:"event_type"`
	ZoneID     string          `json:"zone_id"`     // 能源区域编码, 空表示未分区
	OccurredAt int64           `json:"occurred_at"` // 事件时间(Unix 秒), 统一契约字段
	Payload    json.RawMessage `json:"payload"`
	Source     string          `json:"source"`
	AreaID     int64           `json:"area_id"`   // 旧契约字段(历史消息兼容)
	Timestamp  int64           `json:"timestamp"` // 旧契约字段, 毫秒(历史消息兼容)
}

// EventTime 返回事件时间的 Unix 秒.
//
// 单位必须与 common/kafka.DeviceTelemetry 一致(秒), 不能各自解释: 告警落库、ES 双写、
// 大屏聚合都按秒消费, 混入毫秒会让时间整体偏移 1000 倍。
//
// 取值顺序: occurred_at(统一契约) → timestamp(历史消息, 毫秒换算为秒) → 0。
// 均缺失时返回 0 而不是当前时间: 指纹降级会把这个值算进哈希, 用"处理时刻"会让
// 重投的同一条消息算出不同指纹, 幂等失效。
func (e DeviceEvent) EventTime() int64 {
	if e.OccurredAt > 0 {
		return e.OccurredAt
	}
	if e.Timestamp > 0 {
		return e.Timestamp / 1000
	}
	return 0
}

// IdempotentID 返回写入 alarm.request_id 的幂等键.
// 优先使用消息自带 request_id; 缺失时按 sha1(deviceId|eventType|eventTime) 生成指纹降级,
// 并打 WARN 计数, 以推动 M1 补齐 request_id (P0-2).
// 禁止用 partition-offset 作幂等键: 重放时 offset 变化会导致重复处理.
func (e DeviceEvent) IdempotentID() string {
	if r := strings.TrimSpace(e.RequestID); r != "" {
		return r
	}
	raw := strings.Join([]string{e.DeviceID, e.EventType, strconv.FormatInt(e.EventTime(), 10)}, "|")
	sum := sha1.Sum([]byte(raw))
	return "fp:" + hex.EncodeToString(sum[:])
}

// MatchIntrusionRule 硬编码告警规则: 门禁设备上报非法闯入.
// device_type 缺失时按 event_type 兜底匹配(D1): intrusion 为门禁专属事件, 不会误命中其他设备类型.
//
// 该规则已在 alarm_rule 表内有等价配置(种子规则①: device_type=access_control + event_type=intrusion),
// 本函数仅作为「表内零启用规则」时的应急回退保留 —— 引擎有启用规则时不会走到这里。
// 语义与 rule.Rule.AppliesTo 的设备类型维度保持一致(事件未上报 device_type 时放行)。
func (e DeviceEvent) MatchIntrusionRule() bool {
	if e.EventType != EventTypeIntrusion {
		return false
	}
	return e.DeviceType == "" || e.DeviceType == DeviceTypeAccessControl
}

// RuleFields 将设备事件转为规则引擎可求值的字段视图.
// Payload 解析失败时返回空映射: 缺字段的条件一律判为未命中, 不影响其它字段的判定.
func (e DeviceEvent) RuleFields() rule.Fields {
	var payload map[string]interface{}
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &payload)
	}
	return rule.Fields{
		EventType:  e.EventType,
		DeviceID:   e.DeviceID,
		DeviceType: e.DeviceType,
		AreaID:     e.AreaID,
		ZoneID:     e.ZoneID,
		TenantID:   e.TenantID,
		Payload:    payload,
	}
}

// ToAlarmFromDraft 按规则引擎产出的草稿构造待落库告警.
// request_id 恒为消息幂等键(L3 去重按 request_id + rule_id 复合唯一索引, 见 alarm 表 uk_request_rule),
// 故同一事件命中多条规则时各生成一条告警且互不被去重; alarm_no 则需带 rule_id 以保证业务编号唯一.
func (e DeviceEvent) ToAlarmFromDraft(d *rule.Draft, idempotentID string, at time.Time) *model.Alarm {
	a := &model.Alarm{
		AlarmNo:   NewAlarmNo(idempotentID+"#"+strconv.FormatInt(d.RuleID, 10), at),
		RuleID:    d.RuleID,
		DeviceID:  e.DeviceID,
		AreaID:    e.AreaID,
		EventType: e.EventType,
		Level:     d.Level,
		Status:    model.AlarmStatusPending,
		Content:   d.Content,
		RequestID: idempotentID,
	}
	a.TenantID = e.TenantID
	a.CreatedAt = at
	a.UpdatedAt = at
	return a
}

// ToAlarm 将命中的设备事件转换为待落库的告警记录(等级 P2).
func (e DeviceEvent) ToAlarm() *model.Alarm {
	now := time.Now()
	id := e.IdempotentID()
	a := &model.Alarm{
		AlarmNo:   NewAlarmNo(id, now),
		RuleID:    0, // 0 表示硬编码规则(规则引擎上线前)
		DeviceID:  e.DeviceID,
		AreaID:    e.AreaID,
		EventType: e.EventType,
		Level:     model.AlarmLevelMinor,
		Status:    model.AlarmStatusPending,
		Content:   fmt.Sprintf("门禁设备 %s 检测到非法闯入", e.DeviceID),
		RequestID: id,
	}
	// BaseModel 为嵌入字段, Go 不允许在复合字面量中直接赋值提升字段.
	a.TenantID = e.TenantID
	a.CreatedAt = now
	a.UpdatedAt = now
	return a
}

// NewAlarmNo 生成业务唯一的告警编号: AL + yyyymmdd + 幂等键哈希前 8 位.
// 不查库自增序列, 避免额外查询与并发争用; 唯一性由 uk_alarm_no 兜底.
func NewAlarmNo(idempotentID string, at time.Time) string {
	sum := sha1.Sum([]byte(idempotentID))
	return "AL" + at.Format("20060102") + hex.EncodeToString(sum[:])[:8]
}

// HandleDeviceEvent 处理一条设备遥测消息: 解析 -> 规则引擎评估 -> L1 幂等 -> 落库(L3 唯一索引兜底).
// 返回 nil 才提交 Kafka 位移(at-least-once + 消费端幂等 = 有效 exactly-once).
func (s *ServiceContext) HandleDeviceEvent(ctx context.Context, msg kafkago.Message) error {
	log := logx.WithContext(ctx)

	ev, err := parseDeviceEvent(msg.Value)
	if err != nil {
		// 坏消息(格式错误/必填缺失)不可重试: 上抛给死信兜底层落台账, 由人工重放处理,
		// 不再静默丢弃(docs/m3/06 §5.1: 仅日志 = 不可重放, 不推荐).
		log.Errorf("alarm malformed device event topic=%s partition=%d offset=%d err=%v payload=%s",
			msg.Topic, msg.Partition, msg.Offset, err, msg.Value)
		return fmt.Errorf("%w: %v", ErrMalformedEvent, err)
	}
	id := ev.IdempotentID()

	drafts, err := s.evaluateRules(ctx, ev, id)
	if err != nil {
		// 规则评估失败(规则库/窗口计数不可用)不提交位移, 交由消费端重投.
		log.Errorf("alarm evaluate rules failed, requeue later request_id=%s err=%v", id, err)
		return err
	}
	if len(drafts) == 0 {
		// 未命中规则的事件按设计丢弃, 但不能静默: 未命中量突增意味着 M1 事件格式变动或规则缺失,
		// 是"应该报却不报"的漏报现场, 必须留计数日志(docs/m3/06 §3).
		log.Infof("alarm rule not matched, skip device_id=%s device_type=%s event_type=%s",
			ev.DeviceID, ev.DeviceType, ev.EventType)
		return nil
	}
	if s.Alarms == nil || s.Dedup == nil {
		return errors.New("alarm: storage or dedup component not initialized")
	}
	if strings.HasPrefix(id, "fp:") {
		// logx 无 Warn 级别, Slowf 即 WARN.
		log.Slowf("alarm device event missing request_id, fallback to fingerprint device_id=%s", ev.DeviceID)
	}
	if ev.TenantID == 0 {
		// M1 的两条上报通道(event-dispatcher / device-service)目前都不带 tenant_id,
		// 告警会以 tenant_id=0 落库: 后台列表与大屏按租户查不到它, WS 广播也会因
		// "宁可不推也不推错园区"而跳过(见 broadcastAlarmCreated)。
		// 本服务不维护设备主数据, **无法反查租户, 也不允许臆造** —— 所以这里只能留痕:
		// 这条日志是"告警产生了却没人看得到"的唯一线索, 静默吞掉等于安防主链路黑盒化。
		// 彻底解决依赖 P0-13(M1 明确 DeviceEvent v1 必填 tenant_id)。
		log.Slowf("alarm device event missing tenant_id, alarm will be invisible to tenant-scoped queries device_id=%s event_type=%s",
			ev.DeviceID, ev.EventType)
	}

	// L1 消息级幂等: Redis 不可用必须返回 error, 由消费端重投, 不降级放行.
	seen, err := s.Dedup.Seen(ctx, keyDedup+id)
	if err != nil {
		log.Errorf("alarm dedup unavailable, requeue later request_id=%s err=%v", id, err)
		return err
	}
	if seen {
		log.Infof("alarm skip duplicated device event request_id=%s", id)
		return nil
	}
	// 走到这里说明本次调用刚刚占下了 L1 幂等键(SetNX 写入成功)。
	// 从这一刻起, 只要告警没有真正落库, 就必须把键释放掉(见 releaseDedupKey):
	// 否则后续重投会被 L1 判成"已处理"直接返回 nil —— 告警永久丢失、不进死信台账,
	// 日志还伪装成一次正常去重, 运维从现象上完全看不出曾经失败过。
	// 这是 common/dedup 幂等键语义铁律在本链路的落点: 键存在必须等价于"告警确实落库了"。
	fail := func(err error) error {
		s.releaseDedupKey(ctx, id)
		return err
	}

	// 命中多条规则时逐条落库; L2 冷却按"设备 + 事件 + 规则"维度抑制, 不同规则互不掩盖.
	now := time.Now()
	for _, d := range drafts {
		if s.Cooldown != nil {
			cooling, err := s.Cooldown.TryAcquire(ctx, cooldownKey(ev, d.RuleID), cooldownTTL)
			if err != nil {
				log.Errorf("alarm cooldown unavailable, requeue later device_id=%s err=%v", ev.DeviceID, err)
				return fail(err)
			}
			if cooling {
				log.Infof("alarm suppressed by cooldown device_id=%s event_type=%s rule_id=%d",
					ev.DeviceID, ev.EventType, d.RuleID)
				continue
			}
		}

		a := ev.ToAlarmFromDraft(d, id, now)
		if err := s.Alarms.Create(ctx, a); err != nil {
			if errors.Is(err, model.ErrDuplicateRequest) {
				// L3 唯一索引命中(Redis key 过期但库里已有), 视为已处理.
				log.Infof("alarm skip duplicated insert request_id=%s rule_id=%d", id, d.RuleID)
				continue
			}
			log.Errorf("alarm create failed request_id=%s rule_id=%d err=%v", id, d.RuleID, err)
			return fail(err)
		}
		log.Infof("alarm created alarm_no=%s device_id=%s event_type=%s level=%d rule_id=%d",
			a.AlarmNo, a.DeviceID, a.EventType, a.Level, a.RuleID)
		s.broadcastAlarmCreated(ev, a)
		s.indexAlarmDoc(ctx, a)
		s.notifyAlarmCreated(ctx, a, now)
	}
	return nil
}

// releaseDedupKey 释放 L1 幂等键, 使同一条消息可被重新处理(消费端重投 / 死信重放).
//
// 与 Dedup.Seen 配对使用, 只出现在"已占键但告警未落库"的失败路径上:
// 不释放的话, 重投会被 Seen 判为已处理直接返回 nil, 告警就此消失且不进死信;
// 释放后重投是安全的 —— 已落库的那几条由 L3 唯一索引(uk_request_rule)拦截, 不会重复.
//
// 释放失败只记日志: 最坏情况是回到修复前的行为(重投被跳过), 而释放成功才是新增的保障,
// 不能因为释放这一步失败, 就把原本可重试的错误升级成"必须进死信"。
func (s *ServiceContext) releaseDedupKey(ctx context.Context, id string) {
	if s.Dedup == nil {
		return
	}
	if err := s.Dedup.Release(ctx, keyDedup+id); err != nil {
		logx.WithContext(ctx).Errorf("alarm release dedup key failed, retry may be skipped request_id=%s err=%v", id, err)
	}
}

// notifyAlarmCreated 告警落库后向 M5 投递"告警产生"事件(#40 的上半段).
//
// **失败只记日志, 既不回滚告警也不阻塞位移** —— 与 resolve 路径(返回错误码让调用方补偿)
// 刻意不同, 原因是这条路径重投没有意义:
//
//	告警已落库 → 消费端重投 → L1 幂等键命中(或 L3 唯一索引命中)→ 本函数根本不会再被执行,
//	通知永远补不上, 却让整条消息反复重试直至进死信。相比之下"告警在库里、M5 少收一条事件"
//	是可接受的损失, 且落库成功日志已给出 alarm_no, 可人工补发。
//
// 与广播/ES 双写同一取舍: 告警已落 MySQL(唯一事实来源), 旁路失败不应当阻塞主链路。
func (s *ServiceContext) notifyAlarmCreated(ctx context.Context, a *model.Alarm, at time.Time) {
	if s.Notifier == nil {
		// 未配置 Kafka: 部署时的选择, 启动日志已打印, 此处再逐条刷日志没有意义.
		return
	}
	nEv := notify.AlarmEvent{
		AlarmID:   a.AlarmNo,
		Action:    notify.ActionCreated,
		AlarmType: a.EventType,
		DeviceID:  a.DeviceID,
		TenantID:  a.TenantID,
		AreaID:    a.AreaID,
		Severity:  a.Level,
		Status:    a.Status,
		Content:   a.Content,
		Timestamp: at.UnixMilli(),
	}
	if err := s.Notifier.AlarmCreated(ctx, nEv); err != nil {
		logx.WithContext(ctx).Errorf(
			"notify m5 alarm created failed alarm_no=%s request_id=%s device_id=%s err=%v",
			a.AlarmNo, a.RequestID, a.DeviceID, err)
	}
}

// indexAlarmDoc 将告警写入 ES 检索副本(#44 双写).
//
// 失败只记日志、不影响消费位移: 告警已落 MySQL(唯一事实来源), 因"检索副本写失败"而重投
// 会引入重复告警风险; 检索侧本身也可降级 MySQL 兜底. 但必须 Errorf 留痕 ——
// 双写长期失败等于历史检索静默退化, 这类问题只有日志能暴露.
func (s *ServiceContext) indexAlarmDoc(ctx context.Context, a *model.Alarm) {
	if s.Search == nil {
		return
	}
	doc := search.Doc{
		AlarmID:    a.ID,
		TenantID:   a.TenantID,
		AlarmNo:    a.AlarmNo,
		DeviceID:   a.DeviceID,
		AreaID:     a.AreaID,
		EventType:  a.EventType,
		Level:      a.Level,
		Status:     a.Status,
		Content:    a.Content,
		CreateTime: a.CreatedAt,
	}
	if err := s.Search.Index(ctx, doc); err != nil {
		logx.WithContext(ctx).Errorf("alarm index to es failed alarm_no=%s request_id=%s err=%v",
			a.AlarmNo, a.RequestID, err)
	}
}

// broadcastAlarmCreated 告警入库后异步广播(#42): 推送是旁路, 失败不影响落库结果.
// 租户缺失时跳过 —— 宁可不推, 也不能把告警推给别的园区.
func (s *ServiceContext) broadcastAlarmCreated(ev *DeviceEvent, a *model.Alarm) {
	if s.Hub == nil || a.TenantID == 0 {
		return
	}
	s.Hub.Push(a.TenantID, ws.NewEnvelope(ws.TypeAlarmCreated, ws.AlarmEvent{
		AlarmID:   a.ID,
		DeviceID:  a.DeviceID,
		EventType: a.EventType,
		Level:     a.Level,
		Content:   a.Content,
		AreaID:    a.AreaID,
		Status:    a.Status,
	}, ev.IdempotentID()))
}

// HandleWithDeadLetter 是 Kafka 消费入口(docs/m3/06 §5): 在 HandleDeviceEvent 之外包一层
// "可重试错误本地退避重试 → 仍失败落死信台账"的兜底.
//
// 关键约束:
//  1. 坏消息(反序列化失败/必填缺失)不重试, 直接落台账;
//  2. 可重试错误(存储/去重/冷却不可用)重试 3 次后仍失败才落台账;
//  3. 落台账后**必须返回 nil 以提交位移** —— 否则单条坏消息会卡死整个分区(毒丸, §5);
//  4. 台账本身写失败返回 error: 此时宁可不提交位移(可重投), 也不能静默丢消息.
func (s *ServiceContext) HandleWithDeadLetter(ctx context.Context, msg kafkago.Message) error {
	log := logx.WithContext(ctx)

	var lastErr error
	retryCount := 0
	for attempt := 0; ; attempt++ {
		err := s.HandleDeviceEvent(ctx, msg)
		if err == nil {
			return nil
		}
		lastErr = err

		// 坏消息: 重试无意义, 直接进死信.
		if errors.Is(err, ErrMalformedEvent) {
			break
		}
		if attempt >= len(retryBackoff) {
			break
		}
		retryCount++
		log.Errorf("alarm handle device event failed, retry %d/%d after %s err=%v",
			retryCount, len(retryBackoff), retryBackoff[attempt], err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryBackoff[attempt]):
		}
	}

	log.Errorf("alarm send message to dead letter topic=%s partition=%d offset=%d retries=%d err=%v",
		msg.Topic, msg.Partition, msg.Offset, retryCount, lastErr)
	return s.recordDeadLetter(ctx, msg, lastErr, retryCount)
}

// recordDeadLetter 将失败消息写入死信台账.
// 台账未初始化(MySQL 没配)时只能落日志并返回 error —— 让消息保持未提交状态,
// 由运维介入, 绝不在"没地方存"的情况下假装处理成功.
func (s *ServiceContext) recordDeadLetter(ctx context.Context, msg kafkago.Message, cause error, retryCount int) error {
	if s.DeadLetters == nil {
		return fmt.Errorf("alarm: dead letter store unavailable, keep message uncommitted: %w", cause)
	}

	now := time.Now()
	entry := &model.AlarmDLQ{
		Topic:       msg.Topic,
		PartitionNo: msg.Partition,
		MsgOffset:   msg.Offset,
		Payload:     string(msg.Value),
		ErrorMsg:    truncate(cause.Error(), 512),
		RetryCount:  retryCount,
		Status:      model.DLQStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	// 尽力补全定位信息: 坏消息可能解析不出, 此时留零值(不因补全失败影响入账).
	if ev, err := parseDeviceEvent(msg.Value); err == nil {
		entry.TenantID = ev.TenantID
		entry.RequestID = ev.IdempotentID()
		entry.DeviceID = ev.DeviceID
		entry.EventType = ev.EventType
	}

	if err := s.DeadLetters.Create(ctx, entry); err != nil {
		return fmt.Errorf("alarm: write dead letter failed: %w", err)
	}
	// 已入台账即认为该消息处理完毕(提交位移), 避免毒丸卡分区.
	return nil
}

// ReplayDeadLetter 重放一条死信(docs/m3/06 §5.4): 用台账里保存的原始报文重新走一遍消费主链路.
//
// 为什么先删 L1 幂等键: 死信多是"过了 L1 之后的下游步骤失败"产生的, 此时 alarm:dedup:{requestId}
// 仍在 24h TTL 内。若不清键, 重放会被 L1 判成"已处理"直接返回 nil,
// 表现为"点了重放却什么都没发生" —— 这是最难排查的一类静默失败。
// 人工重放是运维的显式意图, 应当真正重新处理; 重复落库由 L3 唯一索引(uk_request_rule)兜底, 不依赖 L1.
func (s *ServiceContext) ReplayDeadLetter(ctx context.Context, entry *model.AlarmDLQ) error {
	if s.Redis != nil && entry.RequestID != "" {
		if err := s.Redis.Del(ctx, keyDedup+entry.RequestID).Err(); err != nil {
			// 清键失败不阻断重放: 最坏情况是重放被 L1 跳过, 由上层据返回值判断.
			logx.WithContext(ctx).Errorf("alarm replay clear dedup key failed request_id=%s err=%v",
				entry.RequestID, err)
		}
	}
	msg := kafkago.Message{
		Topic:     entry.Topic,
		Partition: entry.PartitionNo,
		Offset:    entry.MsgOffset,
		Value:     []byte(entry.Payload),
	}
	return s.HandleDeviceEvent(ctx, msg)
}

// truncate 截断超长文本, 避免 error_msg 超过列宽导致整条死信写不进去.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// evaluateRules 评估事件命中的告警规则(docs/m3/07 §4).
// 规则引擎未接入或未配置启用规则时, 回退到 P0 的硬编码门禁闯入规则,
// 保证"规则中心不可用"时安防主链路不失效(降级但不静默: 回退路径有独立日志).
func (s *ServiceContext) evaluateRules(ctx context.Context, ev *DeviceEvent, idempotentID string) ([]*rule.Draft, error) {
	if s.Engine != nil && s.Engine.HasRules(ctx) {
		return s.Engine.Evaluate(ctx, ev.RuleFields(), idempotentID)
	}
	// 回退由配置显式控制(Rule.DisableLegacyFallback), 不再隐式兜底:
	// 关闭时必须留 WARN —— 否则"规则未生效"和"没有匹配事件"在现象上完全一样, 无法区分.
	if s.Config.Rule.DisableLegacyFallback {
		logx.WithContext(ctx).Slowf(
			"alarm no enabled rule and legacy fallback disabled, event dropped device_id=%s device_type=%s event_type=%s",
			ev.DeviceID, ev.DeviceType, ev.EventType)
		return nil, nil
	}
	if !ev.MatchIntrusionRule() {
		return nil, nil
	}
	// 回退本身必须留痕: 它意味着"规则表里没有任何启用规则", 通常不是正常状态
	// (种子规则①即为门禁闯入规则)。不打日志时, 告警照常产生, 没人会知道它绕过了规则中心 ——
	// 现象与"规则配好了"完全一致, 事后无法区分。
	logx.WithContext(ctx).Slowf(
		"alarm legacy hardcoded intrusion rule in use, alarm_rule has no enabled rule device_id=%s device_type=%s event_type=%s",
		ev.DeviceID, ev.DeviceType, ev.EventType)
	// 等级与规则表内的门禁闯入规则①保持一致(AlarmLevelMajor=3 "严重"):
	// 回退路径只是"规则中心不可用时的兜底", 同一个现象在两条路径上必须产出同一等级,
	// 否则规则一挂, 告警等级就从 3 掉到 2 —— 大屏的声光阈值与 M5 派单优先级都会跟着变。
	return []*rule.Draft{{
		RuleID:    hardcodedRuleID,
		RuleName:  "硬编码门禁闯入规则",
		Level:     model.AlarmLevelMajor,
		EventType: ev.EventType,
		DeviceID:  ev.DeviceID,
		AreaID:    ev.AreaID,
	}}, nil
}

// cooldownKey 生成 L2 冷却键: alarm:cooldown:{deviceID}:{eventType}:{ruleID}.
// 规则维度参与键是为了让不同规则各自冷却, 避免一条规则触发后掩盖其它规则的告警.
func cooldownKey(ev *DeviceEvent, ruleID int64) string {
	return keyCooldown + strings.Join([]string{ev.DeviceID, ev.EventType, strconv.FormatInt(ruleID, 10)}, ":")
}

// parseDeviceEvent 反序列化设备事件并校验必填字段.
func parseDeviceEvent(body []byte) (*DeviceEvent, error) {
	var ev DeviceEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("unmarshal device event: %w", err)
	}
	if ev.DeviceID == "" {
		return nil, errors.New("device event missing device_id")
	}
	if ev.EventType == "" {
		return nil, errors.New("device event missing event_type")
	}
	return &ev, nil
}
