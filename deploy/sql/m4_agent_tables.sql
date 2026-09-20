-- OnePark 智慧园区 - M4 能源归因智能体 建表
-- 覆盖三张表: agent_run(巡检运行) / agent_tool_call(工具调用轨迹) / agent_suggestion(优化建议)
--
-- 执行方式(任选一种):
--   1) Navicat 选中组内公用库 onepark-smart-park, 直接全选执行
--   2) 命令行: mysql -h 115.191.16.159 -u root -p onepark-smart-park < deploy/sql/m4_agent_tables.sql
--
-- 关于定位(和 M4 原有三张表的区别):
--   energy_reading / billing_rule / bill 是"业务数据", 由 52~62 接口读写, 缺了系统跑不起来
--   这里的三张表是"智能体的运行与产出记录", 缺了不影响能耗模块正常工作
--   所以设计原则是: 智能体旁路运行, 现有服务零改动, 删掉这三张表也只是没有建议而已

-- ============================================================
-- agent_run  每一次巡检的运行记录
-- 定时巡检和手动触发都会写一行, 用来回答"这次跑了没, 跑了多久, 用了多少钱"
-- ============================================================
CREATE TABLE IF NOT EXISTS `agent_run` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `run_no`       VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '运行编号, 如 R20260916-001',
  `trigger_type` VARCHAR(16)  NOT NULL DEFAULT 'cron' COMMENT '触发方式: cron定时 manual手动',
  `scope`        VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '巡检范围, 区域名或空串表示全园区',
  `stat_date`    DATE         NOT NULL COMMENT '统计哪一天的数据',
  `status`       TINYINT      NOT NULL DEFAULT 1 COMMENT '1运行中 2成功 3失败',
  `zone_count`   INT          NOT NULL DEFAULT 0 COMMENT '扫了几个区域',
  `find_count`   INT          NOT NULL DEFAULT 0 COMMENT '发现几条异常',
  `llm_enabled`  TINYINT      NOT NULL DEFAULT 0 COMMENT '本次是否调用了大模型',
  `llm_model`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '用的哪个模型, 没调用就是空',
  `tokens_used`  INT          NOT NULL DEFAULT 0 COMMENT '消耗的 token 数, 用来算成本',
  `cost_ms`      INT          NOT NULL DEFAULT 0 COMMENT '整个巡检耗时(毫秒)',
  `error_msg`    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '失败原因',
  `started_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '开始时间',
  `finished_at`  DATETIME     DEFAULT NULL COMMENT '结束时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_run_no` (`run_no`),
  KEY `idx_status_date` (`status`, `stat_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='智能体巡检运行记录';

-- ============================================================
-- agent_tool_call  工具调用轨迹
-- 智能体的判断不是凭空来的, 每一步调了什么工具、拿到什么结果都记下来,
-- 这样出问题时能复盘"它是怎么得出这个结论的", 也方便统计工具成功率
-- ============================================================
CREATE TABLE IF NOT EXISTS `agent_tool_call` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `run_id`     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '属于哪次巡检',
  `tool_name`  VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '工具名, 如 query_zone_daily / query_device_history',
  `params`     JSON         DEFAULT NULL COMMENT '入参',
  `result_sum` VARCHAR(512) NOT NULL DEFAULT '' COMMENT '结果摘要, 不存全量返回避免表太大',
  `duration_ms` INT         NOT NULL DEFAULT 0 COMMENT '耗时(毫秒)',
  `success`    TINYINT      NOT NULL DEFAULT 1 COMMENT '1成功 0失败',
  `error_msg`  VARCHAR(256) NOT NULL DEFAULT '' COMMENT '失败原因',
  `created_at` DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_run` (`run_id`),
  KEY `idx_tool_ok` (`tool_name`, `success`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='智能体工具调用轨迹';

-- ============================================================
-- agent_suggestion  优化建议
-- 智能体最终产出的东西。层1规则算出异常, 层2大模型翻译成人话, 都落在这里
-- 人工审批通过后才流转到工单系统(M6 的 workorder-service 目前还是空壳, 先只落建议)
-- ============================================================
CREATE TABLE IF NOT EXISTS `agent_suggestion` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `run_id`       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '属于哪次巡检',
  `stat_date`    DATE         NOT NULL COMMENT '统计日, 用来去重',
  `zone_id`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '区域',
  `device_id`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '相关设备, 全区域类建议为空',
  `category`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '异常类型, 见下面五种',
  `severity`     TINYINT      NOT NULL DEFAULT 2 COMMENT '严重程度: 3高 2中 1低',
  `title`        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '一句话结论, 给人看的',
  `reason`       TEXT         COMMENT '判断依据, 大模型写的人话解释',
  `evidence`     JSON         DEFAULT NULL COMMENT '证据: 基线值/实际值/偏离度/Top贡献设备等结构化数据',
  `confidence`   DOUBLE       NOT NULL DEFAULT 0 COMMENT '置信度 0到1, 由规则层给出',
  `action`       VARCHAR(512) NOT NULL DEFAULT '' COMMENT '建议动作, 如安排夜间巡检该设备',
  `status`       TINYINT      NOT NULL DEFAULT 1 COMMENT '1待审批 2已通过 3已驳回 4已转工单',
  `reviewer`     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '审批人',
  `reviewed_at`  DATETIME     DEFAULT NULL COMMENT '审批时间',
  `workorder_no` VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '转出的工单号, 未转为空',
  `created_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_status` (`status`),
  KEY `idx_zone_cat` (`zone_id`, `category`),
  KEY `idx_run` (`run_id`),
  -- 去重键: 同一天 + 同一区域设备 + 同一类问题, 只留一条。
  -- 没有它的话, 定时任务每天跑、手动又点几次, 建议列表会堆满一模一样的重复项,
  -- 运维翻两页就懒得看了。重复巡检时走"更新"而不是"新增"。
  UNIQUE KEY `uk_dedup` (`stat_date`, `zone_id`, `device_id`, `category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='智能体优化建议';

-- category 五种异常类型(由层1规则引擎判定, 与大模型无关):
--   night_idle     夜间空转: 低水位时段(23点到次日6点)用量明显偏高, 肉眼柱状图最难发现的一类浪费
--   device_spike   单设备突增: 某台设备当天用量远高于它自己的历史基线
--   zone_surge     全区域普涨: 整个区域用量高于基线, 通常不是单台设备的锅
--   meter_backward 读数回退: 累计读数出现下降, 疑似表计故障或换表, 属于数据质量问题
--   data_missing   数据缺失: 该上报的时间段没有数据, 可能是设备离线
--
-- 为什么要有 confidence: 规则层是确定性的, 偏离度越大置信度越高,
-- 低置信度的建议不推送, 避免"狼来了"导致运维不再看这个系统
