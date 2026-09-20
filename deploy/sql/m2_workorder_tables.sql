-- =====================================================================
-- OnePark 智慧园区 - M2 工单域建表 SQL (从原 m2_mysql_tables.sql 拆分)
-- 目标库: workorder_db (init.sql 已预创建; 由 deploy/sql/run-all.sh 编排执行)
-- 对齐 app/workorder-service/internal/model
-- 说明: 全部 CREATE TABLE IF NOT EXISTS, 幂等可重复执行.
-- =====================================================================

USE `workorder_db`;

-- 工单主表: 状态机 0待派单 1处理中 2待验收 3已完成 4已关闭; version 乐观锁.
CREATE TABLE IF NOT EXISTS `work_order` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  order_no      VARCHAR(32)  NOT NULL COMMENT '工单号 WO-YYYYMMDD-XXXX',
  type          TINYINT      NOT NULL COMMENT '1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他',
  title         VARCHAR(128) NOT NULL COMMENT '工单标题',
  description   VARCHAR(1024)          DEFAULT NULL COMMENT '工单描述',
  reporter_id   BIGINT       NOT NULL COMMENT '报修/发起人(user_id)',
  assignee_id   BIGINT       NOT NULL DEFAULT 0 COMMENT '处理人, 未派单为0',
  department_id BIGINT       NOT NULL DEFAULT 0 COMMENT '处理部门',
  status        TINYINT      NOT NULL DEFAULT 0 COMMENT '0待派单 1处理中 2待验收 3已完成 4已关闭',
  priority      TINYINT      NOT NULL DEFAULT 2 COMMENT '1紧急 2普通 3低',
  location      VARCHAR(128)           DEFAULT NULL COMMENT '位置',
  attachments   VARCHAR(1024)          DEFAULT NULL COMMENT 'MinIO 对象Key列表(JSON)',
  version       BIGINT       NOT NULL DEFAULT 0 COMMENT '乐观锁版本',
  finished_at   DATETIME              DEFAULT NULL COMMENT '完成/关闭时间',
  alarm_id      VARCHAR(64)           DEFAULT NULL COMMENT '来源告警ID(告警自动建单时非空), 幂等键',
  created_at    DATETIME     NOT NULL COMMENT '创建时间',
  updated_at    DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_order_no` (order_no),
  UNIQUE KEY `uk_alarm_id` (alarm_id),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_status` (status),
  KEY `idx_tenant_status_assignee` (tenant_id, status, assignee_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单主表';

-- 工单操作流水: 每次流转(建单/派单/提交/验收/驳回/关闭)一条, 用于审计.
CREATE TABLE IF NOT EXISTS `work_order_flow` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT      NOT NULL COMMENT '园区ID',
  work_order_id BIGINT      NOT NULL COMMENT '关联工单ID',
  from_status   TINYINT              DEFAULT NULL COMMENT '流转前状态(-1 表示建单)',
  to_status     TINYINT     NOT NULL COMMENT '流转后状态',
  action        VARCHAR(32) NOT NULL COMMENT '动作: create/assign/submit/approve/reject/close',
  operator_id   BIGINT      NOT NULL COMMENT '操作人(user_id)',
  remark        VARCHAR(512)         DEFAULT NULL COMMENT '备注',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_wo` (work_order_id),
  KEY `idx_tenant` (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单操作流水';

-- 工单附件映射: 数据库仅存 MinIO 对象Key, 文件实体在 MinIO.
CREATE TABLE IF NOT EXISTS `work_order_attachment` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT      NOT NULL COMMENT '园区ID',
  work_order_id BIGINT      NOT NULL COMMENT '关联工单ID',
  object_key    VARCHAR(256) NOT NULL COMMENT 'MinIO 对象Key',
  file_name     VARCHAR(256)          DEFAULT NULL COMMENT '原始文件名',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_wo` (work_order_id),
  KEY `idx_tenant` (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单附件(MinIO 对象映射)';
