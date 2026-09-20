-- =====================================================================
-- OnePark 智慧园区 - M2 公告域建表 SQL (从原 m2_mysql_tables.sql 拆分)
-- 目标库: notice_db (init.sql 已预创建; 由 deploy/sql/run-all.sh 编排执行)
-- 对齐 app/notice-service/internal/model
-- 说明: 全部 CREATE TABLE IF NOT EXISTS, 幂等可重复执行.
-- =====================================================================

USE `notice_db`;

-- 公告/通知: 1通知 2公告 3活动 4停水 5停电; 支持置顶与定时发布.
-- source: 系统自动通知的幂等键(如 workorder:assigned:{event_id}), 人工发布为 NULL.
CREATE TABLE IF NOT EXISTS `notice` (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id    BIGINT       NOT NULL COMMENT '园区ID',
  title        VARCHAR(255) NOT NULL COMMENT '标题',
  content      TEXT                  DEFAULT NULL COMMENT '公告正文',
  type         TINYINT      NOT NULL DEFAULT 1 COMMENT '1通知 2公告 3活动 4停水 5停电',
  publisher_id BIGINT       NOT NULL DEFAULT 0 COMMENT '发布人(0=系统)',
  top          TINYINT      NOT NULL DEFAULT 0 COMMENT '是否置顶 0否 1是',
  status       TINYINT      NOT NULL DEFAULT 1 COMMENT '1草稿 2已发布 3已撤回',
  publish_at   DATETIME              DEFAULT NULL COMMENT '发布时间(NULL=未发布)',
  source       VARCHAR(64)           DEFAULT NULL COMMENT '来源幂等键(系统通知), 人工发布为NULL',
  created_at   DATETIME     NOT NULL COMMENT '创建时间',
  updated_at   DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_source` (source),
  KEY `idx_tenant_status` (tenant_id, status, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告/通知';

-- 公告已读/送达记录: 站内通知渠道按"人"落一条, read_at 为 NULL 表示已送达未读.
CREATE TABLE IF NOT EXISTS `notice_read` (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id  BIGINT   NOT NULL COMMENT '园区ID',
  notice_id  BIGINT   NOT NULL COMMENT '关联公告ID',
  user_id    BIGINT   NOT NULL COMMENT '目标用户ID',
  read_at    DATETIME          DEFAULT NULL COMMENT '已读时间(NULL=未读)',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_notice_user` (notice_id, user_id),
  KEY `idx_user_read` (tenant_id, user_id, read_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告已读/送达记录';
