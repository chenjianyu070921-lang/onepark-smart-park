-- ============================================================================
-- M5 运营招商 + 指挥调度 表结构
-- 库: leasing_db(招商租赁) / dispatch_db(指挥调度)
-- 说明: 每服务独立库, 禁止跨库 JOIN; 金额统一 DECIMAL, 禁止 FLOAT/DOUBLE
-- 应用: docker cp deploy/sql/m5_mysql_tables.sql onepark-mysql:/tmp/m5.sql
--       docker exec onepark-mysql sh -c "mysql -uroot -p<密码> < /tmp/m5.sql"
-- ============================================================================

-- ###########################################################################
-- # leasing_db: 招商租赁
-- ###########################################################################
USE leasing_db;

-- 合同状态: 1 待生效 / 2 生效中 / 3 已到期 / 4 已终止
-- 合法转移见 app/leasing-service/internal/state/fsm.go
CREATE TABLE IF NOT EXISTS `lease_contract` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `contract_no`  VARCHAR(32)     NOT NULL                COMMENT '合同编号, 业务唯一键',
  `tenant_id`    BIGINT UNSIGNED NOT NULL DEFAULT 0      COMMENT '租户(园区)ID, RBAC 隔离维度',
  `tenant_name`  VARCHAR(128)    NOT NULL DEFAULT ''     COMMENT '承租企业名称',
  `zone_code`    VARCHAR(64)     NOT NULL DEFAULT ''     COMMENT '房间/区域编码, 如 A-3F-301',
  `area_sqm`     DECIMAL(10,2)   NOT NULL DEFAULT 0.00   COMMENT '租赁面积(平方米)',
  `monthly_rent` DECIMAL(12,2)   NOT NULL DEFAULT 0.00   COMMENT '月租金',
  `deposit`      DECIMAL(12,2)   NOT NULL DEFAULT 0.00   COMMENT '押金',
  `start_date`   DATE            NOT NULL                COMMENT '起租日',
  `end_date`     DATE            NOT NULL                COMMENT '终止日',
  `status`       TINYINT         NOT NULL DEFAULT 1      COMMENT '1待生效 2生效中 3已到期 4已终止',
  `version`      BIGINT          NOT NULL DEFAULT 0      COMMENT '乐观锁版本',
  `created_at`   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_contract_no` (`contract_no`),
  KEY `idx_status_end_date` (`status`, `end_date`),
  KEY `idx_zone_status` (`zone_code`, `status`),
  KEY `idx_tenant` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 租赁合同';

-- 区域面积表: 入驻率 = SUM(已租面积) / SUM(总面积)
CREATE TABLE IF NOT EXISTS `lease_zone` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `zone_code`      VARCHAR(64)     NOT NULL              COMMENT '区域/楼层编码',
  `zone_name`      VARCHAR(128)    NOT NULL DEFAULT ''   COMMENT '区域名称',
  `total_area_sqm` DECIMAL(10,2)   NOT NULL DEFAULT 0.00 COMMENT '可租总面积(平方米)',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_zone_code` (`zone_code`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 园区可租区域';

-- 合同状态流转审计: 状态机负责"能不能改", 本表负责"改过之后能查"
CREATE TABLE IF NOT EXISTS `lease_contract_status_log` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `contract_id` BIGINT UNSIGNED NOT NULL,
  `from_status` TINYINT         NOT NULL COMMENT '流转前状态',
  `to_status`   TINYINT         NOT NULL COMMENT '流转后状态',
  `action`      VARCHAR(32)     NOT NULL DEFAULT '' COMMENT 'create/activate/expire/terminate/renew',
  `reason`      VARCHAR(512)    NOT NULL DEFAULT '',
  `operator_id` BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '操作人(网关注入的 x-user-id)',
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_contract` (`contract_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 合同状态流转审计';

-- 租金账单: uk_contract_period 是"自动生成账单"幂等的根基(不依赖分布式锁)
CREATE TABLE IF NOT EXISTS `lease_bill` (
  `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `bill_no`        VARCHAR(32)     NOT NULL COMMENT '账单编号',
  `contract_id`    BIGINT UNSIGNED NOT NULL,
  `tenant_id`      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `billing_period` VARCHAR(7)      NOT NULL COMMENT '账期, 格式 yyyy-MM',
  `amount`         DECIMAL(12,2)   NOT NULL DEFAULT 0.00 COMMENT '应收金额',
  `status`         TINYINT         NOT NULL DEFAULT 1 COMMENT '1未缴 2已缴',
  `created_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`     DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_contract_period` (`contract_id`, `billing_period`),
  KEY `idx_period_status` (`billing_period`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 租金账单';

-- ###########################################################################
-- # dispatch_db: 指挥调度
-- ###########################################################################
USE dispatch_db;

-- 调度工单状态: 1 待指派 / 2 已指派 / 3 处理中 / 4 已完成 / 5 已关闭
CREATE TABLE IF NOT EXISTS `dispatch_task` (
  `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `task_no`         VARCHAR(32)     NOT NULL                COMMENT '调度单号, 业务唯一键',
  `title`           VARCHAR(255)    NOT NULL DEFAULT ''     COMMENT '标题',
  `source`          TINYINT         NOT NULL DEFAULT 1      COMMENT '1人工创建 2告警自动创建',
  -- alarm_id 必须可空: MySQL 唯一索引允许多个 NULL, 若用 '' 默认值会导致第二张人工单唯一键冲突
  `alarm_id`        VARCHAR(64)     NULL DEFAULT NULL       COMMENT '来源告警ID, 自动建单幂等键',
  `zone_code`       VARCHAR(64)     NOT NULL DEFAULT ''     COMMENT '事发区域, 用于就近指派',
  `priority`        TINYINT         NOT NULL DEFAULT 3      COMMENT '1紧急 2高 3普通',
  `status`          TINYINT         NOT NULL DEFAULT 1      COMMENT '1待指派 2已指派 3处理中 4已完成 5已关闭',
  `assignee_id`     BIGINT UNSIGNED NOT NULL DEFAULT 0      COMMENT '处理人ID',
  `assignee_name`   VARCHAR(64)     NOT NULL DEFAULT ''     COMMENT '处理人姓名(冗余便于列表展示)',
  `description`     VARCHAR(1024)   NOT NULL DEFAULT ''     COMMENT '描述',
  `assign_expire_at` DATETIME       NULL                    COMMENT '指派超时时间, 超时由 cron 重派',
  `finished_at`     DATETIME        NULL                    COMMENT '完成时间',
  `version`         BIGINT          NOT NULL DEFAULT 0      COMMENT '乐观锁版本',
  `created_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_task_no` (`task_no`),
  UNIQUE KEY `uk_alarm_id` (`alarm_id`),
  KEY `idx_status_priority` (`status`, `priority`),
  KEY `idx_zone_status` (`zone_code`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 调度工单';

-- 调度工单状态流转审计
CREATE TABLE IF NOT EXISTS `dispatch_task_log` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `task_id`     BIGINT UNSIGNED NOT NULL,
  `from_status` TINYINT         NOT NULL,
  `to_status`   TINYINT         NOT NULL,
  `action`      VARCHAR(32)     NOT NULL DEFAULT '' COMMENT 'create/assign/start/finish/close',
  `remark`      VARCHAR(512)    NOT NULL DEFAULT '',
  `operator_id` BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `created_at`  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_task` (`task_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='M5 调度工单流转审计';
