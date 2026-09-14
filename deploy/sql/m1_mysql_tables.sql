-- ============================================================
-- OnePark M1 物联接入底座 - MySQL 业务表
-- 适用数据库：onepark-smart-park（4张表全放一个库）
-- 执行方式：Navicat 直接执行
-- 约定：不建物理外键，仅逻辑关联
-- ============================================================

-- ############################################################
-- 1. product 产品表
-- ############################################################
DROP TABLE IF EXISTS `product`;
CREATE TABLE `product` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `product_key` VARCHAR(32)  NOT NULL COMMENT '产品唯一标识, 如 pk_electric_meter',
  `product_name` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '产品名称',
  `description` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '产品描述',
  `thing_model` JSON         NULL COMMENT '物模型 JSON: properties/events/services',
  `status`      TINYINT      NOT NULL DEFAULT 1 COMMENT '1启用 0禁用',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  `deleted_at`  DATETIME     NULL DEFAULT NULL COMMENT '软删除时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_product_key` (`product_key`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='产品表(一类设备的模板, 挂物模型JSON)';

-- 物模型 thing_model JSON 结构示例:
-- {
--   "properties": [{"name":"voltage","type":"double","unit":"V","readWrite":"r"}],
--   "events":     [{"name":"over_voltage","level":"warn"}],
--   "services":   [{"name":"reboot","input":[],"output":[]}]
-- }

-- ############################################################
-- 2. device 设备表
-- ############################################################
DROP TABLE IF EXISTS `device`;
CREATE TABLE `device` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `device_id`       CHAR(36)     NOT NULL COMMENT 'UUID 设备唯一标识',
  `device_name`     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备名称',
  `device_secret`   VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'bcrypt 加密后的设备密钥',
  `product_key`     VARCHAR(32)  NOT NULL COMMENT '所属产品 key(逻辑关联 product.product_key)',
  `park_id`         VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '园区 ID',
  `building_id`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '楼栋 ID',
  `floor`           VARCHAR(16)  NOT NULL DEFAULT '' COMMENT '楼层',
  `location`        VARCHAR(128) NOT NULL DEFAULT '' COMMENT '具体位置描述',
  `status`          TINYINT      NOT NULL DEFAULT 0 COMMENT '0离线 1在线 2禁用',
  `last_online_at`  DATETIME     NULL DEFAULT NULL COMMENT '最后在线时间',
  `created_at`      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  `deleted_at`      DATETIME     NULL DEFAULT NULL COMMENT '软删除时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_device_id` (`device_id`),
  KEY `idx_product_status` (`product_key`, `status`),
  KEY `idx_park` (`park_id`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='设备表(设备主数据, 软删除)';

-- ############################################################
-- 3. command_log 指令下发日志表
-- ############################################################
DROP TABLE IF EXISTS `command_log`;
CREATE TABLE `command_log` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `request_id`    CHAR(36)     NOT NULL COMMENT '幂等请求 ID (UUID)',
  `device_id`     CHAR(36)     NOT NULL COMMENT '目标设备 ID',
  `command_type`  VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '指令类型: setProperty/invoke/reboot...',
  `payload`       JSON         NOT NULL COMMENT '指令内容 JSON',
  `mode`          TINYINT      NOT NULL DEFAULT 1 COMMENT '1同步 2异步',
  `status`        TINYINT      NOT NULL DEFAULT 0 COMMENT '0待发送 1已发送 2已执行 3超时 4失败',
  `response`      JSON         NULL COMMENT '设备回执 JSON',
  `sent_at`       DATETIME     NULL DEFAULT NULL COMMENT '发送时间',
  `executed_at`   DATETIME     NULL DEFAULT NULL COMMENT '设备执行回执时间',
  `timeout_at`    DATETIME     NOT NULL COMMENT '超时时间',
  `created_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_request_id` (`request_id`),
  KEY `idx_device_status` (`device_id`, `status`),
  KEY `idx_timeout` (`status`, `timeout_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='指令下发日志(幂等+超时扫描)';

-- ############################################################
-- 4. shadow 设备影子表
-- ############################################################
DROP TABLE IF EXISTS `shadow`;
CREATE TABLE `shadow` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `device_id`   CHAR(36)     NOT NULL COMMENT '设备 ID',
  `desired`     JSON         NULL COMMENT '期望状态(管理员设的待执行指令)',
  `reported`    JSON         NULL COMMENT '上报状态(设备最新状态)',
  `version`     INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '乐观锁版本号',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_device_id` (`device_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='设备影子表(desired/reported 双状态)';

-- ============================================================
-- 验证
-- ============================================================
SHOW TABLES;