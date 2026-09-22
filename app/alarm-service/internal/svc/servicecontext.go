package svc

import (
	"context"
	"log"
	"strings"
	"time"

	"onepark/app/alarm-service/internal/config"
	"onepark/app/alarm-service/internal/dedup"
	"onepark/app/alarm-service/internal/dispatch"
	"onepark/app/alarm-service/internal/model"
	"onepark/app/alarm-service/internal/notify"
	"onepark/app/alarm-service/internal/rule"
	"onepark/app/alarm-service/internal/search"
	"onepark/app/alarm-service/internal/ws"
	"onepark/common/gormx"
	"onepark/common/kafka"
	"onepark/common/redisx"
)

// dedupTTL 消息级幂等键有效期(L1), 与 docs/m3/06 §4 一致.
const dedupTTL = 24 * 60 * 60 * 1e9 // 24h, 单位纳秒(time.Duration)

// cooldownTTL 业务冷却窗口(L2, docs/m3/08 §1): 60s, 抑制设备抖动导致的重复告警.
const cooldownTTL = 60 * time.Second

// ServiceContext 持有 alarm-service 运行时的全局依赖.
// Alarms/Dedup 抽成接口是为了让消费链路脱离 MySQL/Redis 可单测(见 internal/svc/consumer_test.go).
// ruleCacheTTL 规则快照缓存时间: 规则变更低频, 避免每条事件都查库(docs/m3/07 §4).
const ruleCacheTTL = 30 * time.Second

type ServiceContext struct {
	Config      config.Config
	DeadLetters model.DeadLetterModel // 消费死信台账(docs/m3/06 §5)
	// Hub 告警 WebSocket 广播中心(纯内存, 不依赖中间件, 始终可用).
	Hub      *ws.Hub
	DB       *gormx.DB            // GORM MySQL 连接(alarm_db)
	Redis    *redisx.Client       // Redis 客户端(幂等去重 / 冷却窗口 / 滑动窗口)
	Alarms   model.AlarmModel     // 告警数据访问层
	Rules    model.AlarmRuleModel // 告警规则数据访问层(CRUD + 引擎取规则)
	Engine   *rule.Engine         // 告警规则引擎; 未配置规则时消费链路回退硬编码规则
	Dedup    dedup.Deduper        // 消息级幂等去重(L1)
	Cooldown dedup.Cooldown       // 业务冷却去重(L2)
	// Search 历史告警检索副本(#41 检索 / #44 双写); nil 表示未配置 ES,
	// 检索降级 MySQL, 双写跳过(不视为故障).
	Search search.Searcher
	// Notifier 告警事件通知 M5(#40); nil 表示未配置 Kafka, 通知跳过(不视为故障).
	Notifier notify.Notifier
}

// NewServiceContext 根据配置初始化全局依赖.
// MySQL DSN 未配置时不初始化 DB(本地无中间件仍可启动); 已配置但连接串非法则直接退出, 避免带病启动.
func NewServiceContext(c config.Config) *ServiceContext {
	var db *gormx.DB
	if dsn := unresolvedToEmpty(c.MySQL.DataSource); dsn != "" {
		var err error
		db, err = gormx.NewDB(dsn)
		if err != nil {
			log.Fatalf("init mysql failed: %v", err)
		}
		log.Printf("[info] alarm-service mysql initialized, db=%s", databaseOf(dsn))
	} else {
		log.Printf("[warn] alarm-service mysql data source is empty, db not initialized")
	}

	rds := redisx.NewClient(&c.Redis)

	svcCtx := &ServiceContext{
		Config: c,
		Hub:    ws.NewHub(),
		DB:     db,
		Redis:  rds,
	}
	if db != nil {
		svcCtx.Alarms = model.NewAlarmModel(db)
		svcCtx.Rules = model.NewAlarmRuleModel(db)
		svcCtx.DeadLetters = model.NewDeadLetterModel(db)
		svcCtx.Dedup = dedup.NewRedisDeduper(rds, dedupTTL)
		svcCtx.Cooldown = dedup.NewRedisCooldown(rds)
		// 滑动窗口(time_window 规则)依赖 Redis; 引擎在窗口组件为 nil 时会跳过该类规则.
		svcCtx.Engine = rule.NewEngine(svcCtx.Rules, rule.NewRedisWindow(rds), ruleCacheTTL)
	}
	svcCtx.Search = newSearcher(c)
	svcCtx.Notifier = newNotifier(c)
	return svcCtx
}

// newNotifier 按配置装配告警事件通知器(#40).
// 未配置 Kafka brokers 时返回 nil: 通知是跨模块旁路, 缺 broker 时接口链路照常,
// 但必须在启动日志留痕 —— 否则"告警解决了却没人通知 M5"会是一个很难发现的静默缺失.
func newNotifier(c config.Config) notify.Notifier {
	brokers := unresolvedToEmpty(c.Kafka.Brokers)
	if brokers == "" {
		log.Printf("[warn] alarm-service kafka brokers empty, alarm event notify to m5 disabled")
		return nil
	}
	// 与环境变量未注入时的行为保持一致: 未展开的占位符不能当成真实 topic 名(会发到垃圾 topic 上).
	topic := unresolvedToEmpty(strings.TrimSpace(c.Notify.Topic))
	if topic == "" {
		topic = notify.DefaultTopic
	}
	log.Printf("[info] alarm-service alarm event notify enabled, topic=%s", topic)
	return notify.NewKafkaNotifier(kafka.NewProducer(brokers), topic,
		notify.WithPriorityTable(newPriorityTable(c)))
}

// newPriorityTable 按配置生成"告警等级 → 工单优先级"映射(#40 的派单衔接).
//
// 非法覆盖项在此打 WARN 后按默认继续: 这条旁路不影响服务可启动性,
// 但必须留痕 —— 否则运营改了映射却不生效时没有任何线索.
func newPriorityTable(c config.Config) dispatch.Table {
	table, rejected := dispatch.ParseTable(c.Dispatch.LevelPriority)
	for _, reason := range rejected {
		log.Printf("[warn] alarm-service dispatch priority override rejected: %s", reason)
	}
	if len(c.Dispatch.LevelPriority) > 0 && len(rejected) == 0 {
		log.Printf("[info] alarm-service dispatch priority overrides applied, levels=%d",
			len(c.Dispatch.LevelPriority))
	}
	return table
}

// newSearcher 按配置装配 ES 检索客户端(#41/#44).
// 未配置地址返回 nil(检索降级 MySQL); 已配置但建索引失败只记 WARN ——
// ES 是检索副本而非事实来源, 让它把服务启动挡住是本末倒置.
func newSearcher(c config.Config) search.Searcher {
	es := search.NewESClient(c.ES.Addresses, c.ES.Username, c.ES.Password, c.ES.Index, c.ES.Analyzer)
	if es == nil {
		log.Printf("[info] alarm-service es not configured, history search falls back to mysql")
		return nil
	}
	if err := es.EnsureIndex(context.Background()); err != nil {
		log.Printf("[warn] alarm-service es ensure index failed (search will fall back to mysql): %v", err)
		return es
	}
	log.Printf("[info] alarm-service es initialized, index=%s", es.IndexName())
	return es
}

// StartConsumers 启动后台 Kafka 消费者(设备遥测 -> 安防告警), 独立 goroutine 运行.
// 仅在 MySQL 与 Kafka 均配置时启动: 缺 DB 无法落库, 缺 broker 无法消费, 二者缺一宁不启动也不降级丢告警.
func (s *ServiceContext) StartConsumers(ctx context.Context) {
	if s.Alarms == nil {
		log.Printf("[warn] alarm-service mysql not ready, skip device event consumer")
		return
	}
	brokers := unresolvedToEmpty(s.Config.Kafka.Brokers)
	if brokers == "" {
		log.Printf("[warn] alarm-service kafka brokers empty, skip device event consumer")
		return
	}
	go func() {
		consumer := kafka.NewConsumer(brokers, kafka.TopicDeviceTelemetry, s.Config.Kafka.GroupID)
		defer func() {
			if err := consumer.Close(); err != nil {
				log.Printf("[error] alarm-service close kafka consumer failed: %v", err)
			}
		}()
		log.Printf("[info] alarm-service start consume topic=%s group=%s", kafka.TopicDeviceTelemetry, s.Config.Kafka.GroupID)
		// 消费入口走死信兜底层: 重试 + 落台账, 保证坏消息既不卡分区也不被静默丢弃.
		if err := consumer.Consume(ctx, s.HandleWithDeadLetter); err != nil && ctx.Err() == nil {
			log.Printf("[error] alarm device event consumer exited: %v", err)
		}
	}()
}

// StartWSRelay 启动 WebSocket 跨实例广播(docs/m3/09 §4 方案B).
// 未配置广播频道或 Redis 地址时保持单实例内存广播 —— 明确记 WARN 而不是静默降级,
// 否则多副本部署下"大屏收不到别的实例推的告警"会变成一个很难定位的问题.
func (s *ServiceContext) StartWSRelay(ctx context.Context) {
	channel := strings.TrimSpace(s.Config.WS.BroadcastChannel)
	if channel == "" {
		log.Printf("[warn] alarm-service ws broadcast channel empty, websocket runs in single-instance mode")
		return
	}
	if unresolvedToEmpty(s.Config.Redis.Addr) == "" {
		log.Printf("[warn] alarm-service redis addr empty, websocket cross-instance broadcast disabled")
		return
	}
	s.Hub.StartRelay(ctx, ws.NewRedisRelay(s.Redis, channel))
	log.Printf("[info] alarm-service websocket cross-instance broadcast enabled channel=%s", channel)
}

// unresolvedToEmpty 将未展开的环境变量占位符视为空值, 并去掉首尾空白.
// go-zero 在环境变量缺失时会保留 ${VAR} 字面量, 直接拿去连 MySQL 会报 "invalid DSN";
// 只填了空格的配置(如 Brokers: " ")同样不是有效配置, 一并归一为空串.
func unresolvedToEmpty(v string) string {
	if strings.Contains(v, "${") {
		return ""
	}
	return strings.TrimSpace(v)
}

// databaseOf 从 GORM DSN 中截取数据库名, 仅用于启动日志, 不建立连接.
func databaseOf(dsn string) string {
	// DSN 形如 user:pass@tcp(host:port)/dbname?charset=utf8mb4
	i := strings.LastIndex(dsn, "/")
	if i < 0 || i == len(dsn)-1 {
		return "<unknown>"
	}
	name := dsn[i+1:]
	if j := strings.IndexAny(name, "?&"); j >= 0 {
		name = name[:j]
	}
	return name
}
