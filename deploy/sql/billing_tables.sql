-- =====================================================================
-- M2 billing-service 建表脚本 (billing_db)
-- 说明: 原 billing-service 无任何建表 SQL, 部署时表不存在导致服务不可用;
--       本脚本补齐, 并给 bill 增加 (zone_id, period_start, period_end) 唯一索引,
--       兜底"先查后插"的并发重复出账问题(第 1 轮审查 F1)。
-- 已知遗留: bill / billing_rule 无 tenant_id 字段, 多园区数据未按租户隔离
--           (与 M2 负责人对齐后补迁移, 见 docs 审查记录)。
-- =====================================================================

CREATE DATABASE IF NOT EXISTS `billing_db` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
USE `billing_db`;

-- 计费规则: 一个区域一种计价规则, zone_id 为空表示全园区默认规则
CREATE TABLE IF NOT EXISTS `billing_rule` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `name`        VARCHAR(64)     NOT NULL COMMENT '规则名称',
  `zone_id`     VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '区域编码, 空=全园区默认',
  `rule_type`   TINYINT         NOT NULL DEFAULT 1 COMMENT '1 单一电价 2 阶梯 3 峰谷分时',
  `config`      JSON            NOT NULL COMMENT '规则配置(价格/阶梯/时段), 原文',
  `status`      TINYINT         NOT NULL DEFAULT 1 COMMENT '1 启用 0 停用',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_zone_status` (`zone_id`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M2 计费规则';

-- 账单: 一个区域一个账期只能有一张账单
CREATE TABLE IF NOT EXISTS `bill` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `bill_no`       VARCHAR(32)     NOT NULL DEFAULT '' COMMENT '账单号 B{yyyyMM}-{zoneId}',
  `zone_id`       VARCHAR(64)     NOT NULL DEFAULT '' COMMENT '区域编码',
  `rule_id`       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '计费规则ID(快照来源)',
  `rule_snapshot` JSON            NOT NULL COMMENT '出账时规则配置快照',
  `detail`        JSON            NOT NULL COMMENT '计费明细(JSON), 说明钱怎么算的',
  `period_start`  DATE            NOT NULL COMMENT '账期起',
  `period_end`    DATE            NOT NULL COMMENT '账期止',
  `usage_kwh`     DOUBLE          NOT NULL DEFAULT 0 COMMENT '用电量(度)',
  `amount`        DECIMAL(12,2)   NOT NULL DEFAULT 0 COMMENT '金额(元)',
  `status`        TINYINT         NOT NULL DEFAULT 1 COMMENT '1 待缴 2 已缴 3 作废',
  `paid_at`       DATETIME        NULL COMMENT '缴费时间',
  `created_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_zone_period` (`zone_id`, `period_start`, `period_end`),
  KEY `idx_status_period` (`status`, `period_start`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M2 电费账单';
