-- =====================================================================
-- OnePark 智慧园区 - M2 计费域-电表读数建表 SQL (从原 m2_mysql_tables.sql 拆分)
-- 目标库: billing_db (init.sql/ billing_tables.sql 已预创建; 由 run-all.sh 编排执行)
-- 写入方: M4 energy-data-service; 读取方: billing-service.
-- 说明: CREATE TABLE IF NOT EXISTS, 幂等可重复执行.
-- ⚠️ 归属说明: energy_reading 表逻辑上由 energy-data-service 写入、billing 只读,
--    现行部署将其置于 billing_db(与计费规则/账单同库), energy-data-service 通过
--    ENERGY_DATA_MYSQL_DSN 指向 billing_db 完成写入; 待 energy-data 独立库就绪后迁移.
-- =====================================================================

CREATE DATABASE IF NOT EXISTS `billing_db` DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `billing_db`;

-- 电表读数: 本服务只读, 写入方为 M4 energy-data-service.
CREATE TABLE IF NOT EXISTS `energy_reading` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(0=历史数据/M4侧)',
  device_id   VARCHAR(64) NOT NULL COMMENT '电表设备ID',
  zone_id     VARCHAR(64) NOT NULL COMMENT '区域ID',
  energy_kwh  DOUBLE      NOT NULL COMMENT '累计读数(度)',
  reported_at DATETIME    NOT NULL COMMENT '上报时间',
  PRIMARY KEY (id),
  KEY `idx_zone_time` (zone_id, reported_at),
  KEY `idx_device_time` (device_id, reported_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='电表读数(M4 写入)';
