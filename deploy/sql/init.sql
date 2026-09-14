-- OnePark 智慧园区 - 数据库初始化
-- 容器启动时自动执行, 预创建 20 个业务库 (每服务独立库, 禁止跨库 JOIN)

CREATE DATABASE IF NOT EXISTS device_db        DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS gateway_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS shadow_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS event_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS workorder_db    DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS visitor_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS parking_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS notice_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS alarm_db        DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS access_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS video_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS energy_data_db  DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS energy_analysis_db DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS billing_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS leasing_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS dashboard_db    DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS dispatch_db     DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS auth_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS user_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS gateway_route_db DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- RBAC 公共表 (M6 user_db 内, 五表模型)
USE user_db;
CREATE TABLE IF NOT EXISTS `user` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username    VARCHAR(64)  NOT NULL DEFAULT '',
  password    VARCHAR(255) NOT NULL DEFAULT '',
  nickname    VARCHAR(64)  NOT NULL DEFAULT '',
  phone       VARCHAR(20)  NOT NULL DEFAULT '',
  status      TINYINT      NOT NULL DEFAULT 1 COMMENT '1启用 0禁用',
  created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `role` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)  NOT NULL DEFAULT '',
  code        VARCHAR(64)  NOT NULL DEFAULT '',
  created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `permission` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)  NOT NULL DEFAULT '',
  code        VARCHAR(128) NOT NULL DEFAULT '',
  type        TINYINT      NOT NULL DEFAULT 1 COMMENT '1菜单 2按钮 3接口',
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `user_role` (
  user_id     BIGINT UNSIGNED NOT NULL,
  role_id     BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (user_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `role_permission` (
  role_id        BIGINT UNSIGNED NOT NULL,
  permission_id  BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ============================================================================
-- M2 物业管理服务业务表 (对齐 docs/M2物业管理服务模块设计文档 V1.1 第七章/第九章)
-- 约定: 每服务独立库, 禁止跨库 JOIN; 所有业务表统一携带 tenant_id(RBAC 数据隔离)
--        与时间戳 created_at/updated_at; 工单主表额外 version(乐观锁).
-- ============================================================================

-- ------------------------- workorder_db: 工单域 --------------------------
USE workorder_db;

CREATE TABLE IF NOT EXISTS `work_order` (
  `id`            BIGINT       NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT       NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `order_no`      VARCHAR(32)  NOT NULL                COMMENT '工单号 WO-YYYYMMDD-0001',
  `type`          TINYINT      NOT NULL                COMMENT '1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他',
  `title`         VARCHAR(128) NOT NULL,
  `description`   VARCHAR(1024) DEFAULT '',
  `reporter_id`   BIGINT       NOT NULL                COMMENT '报修/发起人',
  `assignee_id`   BIGINT       NOT NULL DEFAULT 0      COMMENT '处理人, 未派单为0',
  `department_id` BIGINT       NOT NULL DEFAULT 0      COMMENT '处理部门',
  `status`        TINYINT      NOT NULL DEFAULT 0      COMMENT '0待派单 1处理中 2待验收 3已完成 4已关闭',
  `priority`      TINYINT      NOT NULL DEFAULT 2      COMMENT '1紧急 2普通 3低',
  `location`      VARCHAR(128) DEFAULT '',
  `attachments`   VARCHAR(1024) DEFAULT ''             COMMENT 'MinIO 对象Key列表(JSON)',
  `version`       BIGINT       NOT NULL DEFAULT 0      COMMENT '乐观锁版本',
  `finished_at`   DATETIME     DEFAULT NULL            COMMENT '完成/关闭时间',
  `created_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_order_no` (`order_no`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_status` (`status`),
  KEY `idx_status_type_assignee_created` (`status`, `type`, `assignee_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单主表';

CREATE TABLE IF NOT EXISTS `work_order_flow` (
  `id`            BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `work_order_id` BIGINT   NOT NULL                COMMENT '关联工单',
  `from_status`   TINYINT  DEFAULT NULL            COMMENT '流转前状态',
  `to_status`     TINYINT  NOT NULL                COMMENT '流转后状态',
  `action`        VARCHAR(32) NOT NULL             COMMENT 'assign/submit/approve/reject/close',
  `operator_id`   BIGINT   NOT NULL                COMMENT '操作人(网关注入 x-user-id)',
  `remark`        VARCHAR(512) DEFAULT '',
  `created_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_wo` (`work_order_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单操作流水(审计)';

CREATE TABLE IF NOT EXISTS `work_order_attachment` (
  `id`            BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `work_order_id` BIGINT   NOT NULL                COMMENT '关联工单',
  `object_key`    VARCHAR(256) NOT NULL            COMMENT 'MinIO 对象Key',
  `file_name`     VARCHAR(256) DEFAULT '',
  `created_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_wo` (`work_order_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单 MinIO 附件映射(库仅存Key)';

-- ------------------------- visitor_db: 访客域 --------------------------
USE visitor_db;

CREATE TABLE IF NOT EXISTS `visitor_record` (
  `id`            BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `inviter_id`    BIGINT   NOT NULL                COMMENT '邀请人(业主/物业)',
  `visitor_name`  VARCHAR(64) NOT NULL,
  `visitor_phone` VARCHAR(20) DEFAULT '',
  `visit_time`    DATETIME DEFAULT NULL            COMMENT '预期到访时间',
  `expire_time`   DATETIME DEFAULT NULL            COMMENT '二维码过期时间',
  `qr_code`       VARCHAR(512) NOT NULL            COMMENT '加密二维码内容(含签名+有效期)',
  `status`        TINYINT  NOT NULL DEFAULT 1      COMMENT '1待使用 2已签入 3已签出 4已过期',
  `checkin_at`    DATETIME DEFAULT NULL,
  `checkout_at`   DATETIME DEFAULT NULL,
  `device_id`     VARCHAR(64) DEFAULT ''           COMMENT '签入/签出开门设备ID',
  `blacklisted`   TINYINT(1) NOT NULL DEFAULT 0    COMMENT '是否黑名单',
  `created_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_qr_code` (`qr_code`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_status_visit` (`status`, `visit_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='访客通行记录';

-- ------------------------- parking_db: 停车域 --------------------------
USE parking_db;

CREATE TABLE IF NOT EXISTS `parking_record` (
  `id`            BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`     BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `plate_no`      VARCHAR(32) NOT NULL             COMMENT '车牌号',
  `entry_time`    DATETIME DEFAULT NULL            COMMENT '入场时间',
  `exit_time`     DATETIME DEFAULT NULL            COMMENT '离场时间',
  `duration_min`  INT      DEFAULT NULL            COMMENT '停车时长(分钟)',
  `fee`           DECIMAL(10,2) DEFAULT NULL       COMMENT '停车费',
  `vehicle_type`  TINYINT  NOT NULL DEFAULT 1       COMMENT '1月卡 2临时 3VIP 4异常',
  `status`        TINYINT  NOT NULL DEFAULT 1       COMMENT '1停车中 2已完成',
  `device_id_in`  VARCHAR(64) DEFAULT ''           COMMENT '入场地磁设备ID',
  `device_id_out` VARCHAR(64) DEFAULT ''           COMMENT '出场地磁设备ID',
  `created_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_plate_entry` (`plate_no`, `entry_time`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车记录';

CREATE TABLE IF NOT EXISTS `parking_fee_rule` (
  `id`             BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`      BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `rule_json`      JSON                              COMMENT '计费规则(JSON: 首段免费/阶梯单价/封顶等)',
  `effective_from` DATETIME DEFAULT NULL,
  `effective_to`   DATETIME DEFAULT NULL,
  `created_at`     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车计费规则';

-- ------------------------- notice_db: 公告域 --------------------------
USE notice_db;

CREATE TABLE IF NOT EXISTS `notice` (
  `id`           BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`    BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `title`        VARCHAR(255) NOT NULL,
  `content`      TEXT                                COMMENT '公告正文',
  `type`         TINYINT  NOT NULL DEFAULT 1       COMMENT '1通知 2公告 3活动 4停水 5停电',
  `publisher_id` BIGINT   NOT NULL DEFAULT 0      COMMENT '发布人',
  `top`          TINYINT(1) NOT NULL DEFAULT 0    COMMENT '是否置顶',
  `status`       TINYINT  NOT NULL DEFAULT 1      COMMENT '1草稿 2已发布 3已撤回',
  `publish_at`   DATETIME DEFAULT NULL            COMMENT '定时发布时间, NULL=立即',
  `created_at`   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_type_status` (`type`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告/通知';

CREATE TABLE IF NOT EXISTS `notice_read` (
  `id`         BIGINT   NOT NULL AUTO_INCREMENT,
  `tenant_id`  BIGINT   NOT NULL                COMMENT '园区ID, RBAC 数据隔离维度',
  `notice_id`  BIGINT   NOT NULL                COMMENT '关联公告',
  `user_id`    BIGINT   NOT NULL                COMMENT '已读用户',
  `read_at`    DATETIME DEFAULT NULL            COMMENT '已读时间',
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_notice_user` (`notice_id`, `user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告已读记录';
