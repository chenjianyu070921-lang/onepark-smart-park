-- ============================================================
-- OnePark M1 迁移脚本: device 表补 type/latitude/longitude 列
-- 适用: 已按 2026-09 前版本 m1_mysql_tables.sql 建表的存量环境
-- 全新环境无需执行 —— 新版 m1_mysql_tables.sql 已包含这三列与 idx_type.
-- 执行方式: Navicat 直接执行(MySQL 8.x)
-- ============================================================

ALTER TABLE `device`
  ADD COLUMN `type`      TINYINT       NOT NULL DEFAULT 0 COMMENT '设备类型: 1地磁 2门禁 3摄像头..., 0未分类' AFTER `product_key`,
  ADD COLUMN `latitude`  DECIMAL(10,7) NULL DEFAULT NULL COMMENT '纬度(WGS84), 未建档定位为空' AFTER `location`,
  ADD COLUMN `longitude` DECIMAL(10,7) NULL DEFAULT NULL COMMENT '经度(WGS84), 未建档定位为空' AFTER `latitude`,
  ADD KEY `idx_type` (`type`);

-- 存量设备的类型可按产品归类回填, 例如:
-- UPDATE `device` SET `type` = 1 WHERE `product_key` = 'pk_parking_geo';
-- UPDATE `device` SET `type` = 2 WHERE `product_key` = 'pk_door_access';
