-- ============================================================
-- OnePark M3 园区安防 - MySQL 业务表
-- 适用数据库：alarm_db（init.sql 已预创建该库）
-- 执行方式：Navicat 直接执行 / 容器初始化脚本
-- 编码：UTF-8 无 BOM（中文注释在 GBK 连接下会被误解析，执行前请 SET NAMES utf8mb4）
-- 约定：不建物理外键，仅逻辑关联；tenant_id 为 RBAC 行级隔离维度
-- ============================================================
SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;

USE `alarm_db`;

-- ############################################################
-- 1. alarm 告警记录表
-- ############################################################
DROP TABLE IF EXISTS `alarm`;
CREATE TABLE `alarm` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `alarm_no`    VARCHAR(32)  NOT NULL             COMMENT '告警编号(业务唯一) AL+yyyymmdd+8位哈希',
  `rule_id`     BIGINT       NOT NULL DEFAULT 0  COMMENT '命中规则ID(alarm_rule.id), 0表示硬编码回退规则',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备ID(来自 M1, 不维护设备主数据)',
  `area_id`     BIGINT       NOT NULL DEFAULT 0  COMMENT '区域ID',
  `event_type`  VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '事件类型: intrusion/door_forced/temperature',
  `level`       TINYINT      NOT NULL DEFAULT 2  COMMENT '告警等级: 1提示 2一般 3严重 4紧急',
  `status`      TINYINT      NOT NULL DEFAULT 0  COMMENT '0未处理 1已确认 2已解决',
  `content`     VARCHAR(512) NOT NULL DEFAULT '' COMMENT '告警内容',
  `request_id`  VARCHAR(64)  NOT NULL             COMMENT '幂等键(L3兜底): 消息request_id或缺失时指纹',
  `ack_by`      BIGINT       NOT NULL DEFAULT 0  COMMENT '确认人(操作员 user_id)',
  `ack_at`      DATETIME     NULL DEFAULT NULL   COMMENT '确认时间',
  `resolve_by`  BIGINT       NOT NULL DEFAULT 0  COMMENT '解决人',
  `resolve_at`  DATETIME     NULL DEFAULT NULL   COMMENT '解决时间',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_alarm_no` (`alarm_no`),
  -- 复合唯一: 同一事件(request_id)命中不同规则应各生成一条告警, 互不覆盖;
  -- 只有"同一事件 + 同一规则"的重复上报才被拦截.
  UNIQUE KEY `uk_request_rule` (`rule_id`, `request_id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_device` (`device_id`),
  KEY `idx_event_type` (`event_type`),
  KEY `idx_status_level_created` (`status`, `level`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警记录表';

-- 说明: uk_request_id 是三层幂等去重的最底层防线(Redis L1/L2 失效或过期时兜底),
--       同一 request_id 重复上报只会写入一条告警。

-- ############################################################
-- 2. alarm_rule 告警规则表
-- 2026-09-20 同步: 本表是告警规则的唯一事实来源, 消费链路只从本表读取启用规则
--   (model/alarm_rule_model.go#ListEnabled -> internal/rule/engine.go#Evaluate).
--   规则按「设备类型 + 事件类型」二维范围匹配, 命中后按本表 level 生成告警等级:
--     门禁 intrusion 即其中一条普通配置(device_type='access_control' + event_type='intrusion'),
--     改等级 / 禁用都不需要改代码。
--   硬编码门禁闯入规则仅保留为「表内零启用规则」时的应急回退, 由 Rule.DisableLegacyFallback 控制。
-- ############################################################
DROP TABLE IF EXISTS `alarm_rule`;
CREATE TABLE `alarm_rule` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`      BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `name`           VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '规则名称',
  `device_type`    VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '设备类型(access_control/camera/sensor...), 为空表示不限设备类型',
  `device_id`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备ID, 为空表示按 event_type 全局生效',
  `area_id`        BIGINT       NOT NULL DEFAULT 0  COMMENT '区域ID',
  `event_type`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '事件类型',
  `rule_type`      VARCHAR(16)  NOT NULL DEFAULT 'threshold' COMMENT 'threshold 阈值 / composite 组合 / window 时间窗口',
  `conditions`     JSON         NULL COMMENT '规则条件JSON, 由规则引擎 Evaluate',
  `window_seconds` INT          NOT NULL DEFAULT 0  COMMENT '时间窗口(秒), 仅 window/composite 使用',
  `level`          TINYINT      NOT NULL DEFAULT 2  COMMENT '命中后生成的告警等级 1-4',
  `status`         TINYINT      NOT NULL DEFAULT 1  COMMENT '1启用 0禁用',
  `created_at`     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_device_type` (`device_type`),
  KEY `idx_event_type` (`event_type`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警规则表';

-- ############################################################
-- 3. alarm_dlq 死信台账(docs/m3/06 §5.3)
-- 说明: 消费链路最终失败(坏消息或可重试错误耗尽重试)的消息落此表,
--       便于后台按设备/时间排查与人工重放(§5.4). 相比文档补充 tenant_id 保持 RBAC 一致.
--       Kafka DLQ topic(方案 A)为后续补强项, 当前以台账为准(文档: "先做 B").
-- ############################################################
CREATE TABLE IF NOT EXISTS `alarm_dlq` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`    BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID(解析不出时为0)',
  `topic`        VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '源 topic',
  `partition_no` INT          NOT NULL DEFAULT 0  COMMENT '分区号',
  `msg_offset`   BIGINT       NOT NULL DEFAULT 0  COMMENT '原始消息位移',
  `request_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '幂等键(便于关联)',
  `device_id`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备ID(便于按设备排查)',
  `event_type`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '事件类型',
  `payload`      TEXT         NULL                 COMMENT '原始报文',
  `error_msg`    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '失败原因',
  `retry_count`  INT          NOT NULL DEFAULT 0  COMMENT '已重试次数',
  `status`       TINYINT      NOT NULL DEFAULT 0  COMMENT '0待处理/1已重放/2已丢弃',
  `created_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  KEY `idx_status_created` (`status`, `created_at`),
  KEY `idx_device` (`device_id`),
  KEY `idx_request` (`request_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警消费死信台账';

-- ############################################################
-- 4. alarm_operate_log 告警处理流水(审计)
-- ############################################################
DROP TABLE IF EXISTS `alarm_operate_log`;
CREATE TABLE `alarm_operate_log` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `alarm_id`    BIGINT       NOT NULL DEFAULT 0  COMMENT '告警ID',
  `action`      VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'create 生成 / ack 确认 / resolve 解决',
  `operator_id` BIGINT       NOT NULL DEFAULT 0  COMMENT '操作人, create 动作为 0(系统)',
  `remark`      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '备注',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (`id`),
  KEY `idx_alarm` (`alarm_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警处理流水(审计)';

-- ############################################################
-- 5. alarm_rule 初始化种子规则(周一 P2「初始化数据SQL」)
--
-- 为什么必须有: alarm_rule 是规则的唯一事实来源. 表空 + Rule.DisableLegacyFallback=true 时,
--   消费链路会把事件直接丢弃(只打 WARN) —— 部署后若没人手工建规则, 安防主链路等于静默停摆.
--
-- 口径说明:
--   * tenant_id=0 表示平台级默认规则, 引擎加载时不过滤租户, 对所有园区生效;
--     后台规则列表按租户查询, 各园区看不到也改不到这三条, 需要差异化时应另建本园区规则.
--   * 固定主键 + INSERT IGNORE: 重复执行不会产生重复规则(补数据时只跑本段也保持幂等).
--   * 规则①与规则③都匹配 intrusion, 一次闯入可能命中两条(③需窗口内累计到阈值才触发);
--     若不希望默认双报, 把规则③的 status 置 0 即可, 保留它作为时间窗口规则的可用样例.
--   * device_type 是「按设备类型 + 事件类型映射告警等级」的设备类型维度, 为空表示不限;
--     ①③ 限定 access_control, 使"摄像头上报 intrusion"不再被当成门禁闯入(原硬编码规则的语义).
--   * JSON 写法与 internal/rule/rule.go#ParseSpec 的两种兼容格式对齐:
--     ① 用文档 04 的扁平单条件写法, ②③ 用文档 07 的 conditions/match 嵌套写法.
-- ############################################################
INSERT IGNORE INTO `alarm_rule`
  (`id`, `tenant_id`, `name`, `device_type`, `device_id`, `area_id`, `event_type`,
   `rule_type`, `conditions`, `window_seconds`, `level`, `status`)
VALUES
  -- ① 门禁非法闯入(P0 主链路在规则表内的正式版本: 门禁设备 + intrusion -> 等级 2)
  (1, 0, '门禁非法闯入告警', 'access_control', '', 0, 'intrusion', 'threshold',
   '{"type":"threshold","field":"event_type","op":"eq","value":"intrusion"}',
   0, 2, 1),

  -- ② 温度超限(周二 P2 的「温度超过80℃触发告警」示例; 不限设备类型, 由 payload 条件收敛)
  (2, 0, '温度超过80℃告警', '', '', 0, 'temperature', 'threshold',
   '{"type":"threshold","conditions":[{"field":"payload.temperature","op":"gt","value":80}]}',
   0, 3, 1),

  -- ③ 短时反复闯入(周四 P3 / 周六的时间窗口规则: 5 分钟内 ≥3 次)
  (3, 0, '短时反复闯入告警(5分钟≥3次)', 'access_control', '', 0, 'intrusion', 'time_window',
   '{"type":"time_window","window_sec":300,"threshold":3,"match":{"field":"event_type","op":"eq","value":"intrusion"}}',
   300, 3, 1);
