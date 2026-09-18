-- ============================================================
-- OnePark M3 视频 - MySQL 业务表
-- 适用数据库：video_db（init.sql 已预创建该库）
-- 执行方式：Navicat 直接执行 / 容器初始化脚本
-- 编码：UTF-8 无 BOM（中文注释在 GBK 连接下会被误解析，执行前请 SET NAMES utf8mb4）
-- 约定：不建物理外键，仅逻辑关联；tenant_id 为 RBAC 行级隔离维度
-- ============================================================
SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;

USE `video_db`;

-- ############################################################
-- camera 摄像头(docs/m3/04 #49/#50/#51 / §2.3)
-- 说明: M3 只管理摄像头元数据与流地址下发, 不做转码/推流(docs/m3/11).
--       uk_device 保证同一设备(M1 device_id)只登记一次.
--       相比文档补充 tenant_id(RBAC 行级隔离)与 last_heartbeat_at(在线状态,
--       由设备心跳事件更新, #50 列表需要展示).
-- ############################################################
CREATE TABLE IF NOT EXISTS `camera` (
  `id`                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`         BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `name`              VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '摄像头名称',
  `device_id`         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '关联 M1 设备ID',
  `area_id`           BIGINT       NOT NULL DEFAULT 0  COMMENT '区域ID',
  `rtsp_url`          VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'RTSP 拉流地址',
  `location`          JSON         NULL                 COMMENT '位置 {lng,lat,floor}',
  `status`            TINYINT      NOT NULL DEFAULT 0  COMMENT '1在线/0离线/2故障(见 §5.4)',
  `last_heartbeat_at` DATETIME     NULL DEFAULT NULL   COMMENT '最近心跳时间(#50 在线状态)',
  `created_at`        DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`        DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_device` (`tenant_id`, `device_id`),
  -- 心跳按 device_id 单点反查(M1 遥测报文不带 tenant_id, 见 model.TouchHeartbeat):
  -- uk_device 的最左前缀是 tenant_id, 只按 device_id 查询会全表扫描, 故必须单独建索引.
  KEY `idx_device` (`device_id`),
  KEY `idx_area` (`area_id`),
  KEY `idx_status` (`status`),
  KEY `idx_heartbeat` (`last_heartbeat_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='摄像头表';

-- ############################################################
-- video_dlq 心跳消费死信台账(docs/m3/06 §5.3 同款范式)
-- 说明: 摄像头心跳此前遇到坏消息(JSON 解析失败/缺 device_id)只打日志就跳过,
--       既不重试也不可追溯: 上游一旦改报文格式, 在线状态会静默停更且毫无痕迹.
--       入库后可按设备/时间排查并决定是否手工补偿心跳.
--       与 alarm_dlq 的差异: 本表不含 status/updated_at —— 心跳过期即无意义,
--       不做重放/状态流转, 避免留下用不到的字段.
-- ############################################################
CREATE TABLE IF NOT EXISTS `video_dlq` (
  `id`           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `topic`        VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '源 topic',
  `partition_no` INT          NOT NULL DEFAULT 0  COMMENT '分区号',
  `msg_offset`   BIGINT       NOT NULL DEFAULT 0  COMMENT '原始消息位移(定位用)',
  `device_id`    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备ID(解析不出时为空)',
  `payload`      TEXT         NULL                 COMMENT '原始报文',
  `error_msg`    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '失败原因',
  `created_at`   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '入账时间',
  PRIMARY KEY (`id`),
  KEY `idx_created` (`created_at`),
  KEY `idx_device` (`device_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='视频心跳消费死信台账';
