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
  `rule_id`     BIGINT       NOT NULL DEFAULT 0  COMMENT '命中规则ID, 0表示硬编码规则',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备ID(来自 M1, 不维护设备主数据)',
  `area_id`     BIGINT       NOT NULL DEFAULT 0  COMMENT '区域ID',
  `event_type`  VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '事件类型: intrusion/door_forced/temperature',
  `level`       TINYINT      NOT NULL DEFAULT 2  COMMENT '告警等级: 1提示 2一般 3严重 4紧急',
  `status`      TINYINT      NOT NULL DEFAULT 0  COMMENT '0未处理 1已确认 2已解决',
  `content`     VARCHAR(512) NOT NULL DEFAULT '' COMMENT '告警内容',
  `request_id`  VARCHAR(64)  NOT NULL             COMMENT '幂等键(L3唯一索引兜底): 消息request_id或缺失时指纹',
  `ack_by`      BIGINT       NOT NULL DEFAULT 0  COMMENT '确认人(操作员 user_id)',
  `ack_at`      DATETIME     NULL DEFAULT NULL   COMMENT '确认时间',
  `resolve_by`  BIGINT       NOT NULL DEFAULT 0  COMMENT '解决人',
  `resolve_at`  DATETIME     NULL DEFAULT NULL   COMMENT '解决时间',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_alarm_no` (`alarm_no`),
  UNIQUE KEY `uk_request_id` (`request_id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_device` (`device_id`),
  KEY `idx_event_type` (`event_type`),
  KEY `idx_status_level_created` (`status`, `level`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警记录表';

-- 说明: uk_request_id 是三层幂等去重的最底层防线(Redis L1/L2 失效或过期时兜底),
--       同一 request_id 重复上报只会写入一条告警。

-- ############################################################
-- 2. alarm_rule 告警规则表 (P2 动态规则引擎使用, 当前消费走硬编码规则)
-- ############################################################
DROP TABLE IF EXISTS `alarm_rule`;
CREATE TABLE `alarm_rule` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`      BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `name`           VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '规则名称',
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
  KEY `idx_event_type` (`event_type`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='告警规则表';

-- ############################################################
-- 3. alarm_operate_log 告警处理流水(审计)
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
