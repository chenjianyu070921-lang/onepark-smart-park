-- OnePark 智慧园区 - M2 停车域 / 访客域 表结构
-- 适用数据库: parking_db / visitor_db (init.sql 已预创建这两个库)
-- 说明: 此前两域只有 GORM 模型没有建表脚本, 本地起库后表不存在, 接口与消费链路都跑不通.
--       本脚本与 M3 脚本同风格: USE + CREATE TABLE IF NOT EXISTS, 可重复执行.

USE `parking_db`;

-- 1. parking_record 停车记录
-- 入场/离场由 M1 地磁遥测驱动, 亦提供 HTTP 入口便于联调.
CREATE TABLE IF NOT EXISTS `parking_record` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT       NOT NULL DEFAULT 0 COMMENT '园区ID(RBAC 隔离)',
  `plate_no`      VARCHAR(32)  NOT NULL COMMENT '车牌号',
  `entry_time`    DATETIME     NULL COMMENT '入场时间',
  `exit_time`     DATETIME     NULL COMMENT '离场时间',
  `duration_min`  INT          NULL COMMENT '停车时长(分钟)',
  `fee`           DECIMAL(10,2) NOT NULL DEFAULT 0 COMMENT '停车费',
  `vehicle_type`  TINYINT      NOT NULL DEFAULT 1 COMMENT '1月卡 2临时 3VIP 4异常',
  `status`        TINYINT      NOT NULL DEFAULT 1 COMMENT '1停车中 2已完成',
  `device_id_in`  VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '入场地磁设备ID',
  `device_id_out` VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '出场地磁设备ID',
  -- 消费幂等键(L3 兜底): Kafka at-least-once 下同一条遥测重复投递时靠它拦截,
  -- 避免重复建单与重复计费。允许 NULL: HTTP 联调入口不携带幂等键(MySQL 唯一索引允许多个 NULL,
  -- 若用空串会因多行 '' 互相撞键而报 1062).
  `request_id`    VARCHAR(64)  NULL COMMENT '消费幂等键, NULL 表示非消费链路写入',
  `created_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_request` (`request_id`),
  KEY `idx_tenant` (`tenant_id`),
  -- 离场需按"车牌 + 停车中"定位最近一条记录, 该索引直接服务于这条查询.
  KEY `idx_plate_status` (`plate_no`, `tenant_id`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 2. parking_fee_rule 停车计费规则
CREATE TABLE IF NOT EXISTS `parking_fee_rule` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `tenant_id`      BIGINT      NOT NULL DEFAULT 0 COMMENT '园区ID(RBAC 隔离)',
  `rule_json`      JSON        NULL COMMENT '计费规则(JSON)',
  `effective_from` DATETIME    NULL COMMENT '生效起始时间',
  `effective_to`   DATETIME    NULL COMMENT '生效结束时间',
  `created_at`     DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

USE `visitor_db`;

-- 3. visitor_record 访客通行记录
CREATE TABLE IF NOT EXISTS `visitor_record` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT      NOT NULL DEFAULT 0 COMMENT '园区ID(RBAC 隔离)',
  `inviter_id`    BIGINT      NOT NULL DEFAULT 0 COMMENT '邀请人(业主/物业)',
  `visitor_name`  VARCHAR(64) NOT NULL DEFAULT '' COMMENT '访客姓名',
  `visitor_phone` VARCHAR(20) NOT NULL DEFAULT '' COMMENT '访客手机号',
  `visit_time`    DATETIME    NULL COMMENT '预期到访时间',
  `expire_time`   DATETIME    NULL COMMENT '二维码过期时间',
  `qr_code`       VARCHAR(512) NOT NULL COMMENT '加密二维码内容(含签名+有效期)',
  `status`        TINYINT     NOT NULL DEFAULT 1 COMMENT '1待使用 2已签入 3已签出 4已过期',
  `checkin_at`    DATETIME    NULL COMMENT '签入时间',
  `checkout_at`   DATETIME    NULL COMMENT '签出时间',
  `device_id`     VARCHAR(64) NOT NULL DEFAULT '' COMMENT '签入/签出开门设备ID',
  `blacklisted`   TINYINT     NOT NULL DEFAULT 0 COMMENT '是否黑名单 0否 1是',
  `created_at`    DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_qr_code` (`qr_code`),
  KEY `idx_tenant` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
