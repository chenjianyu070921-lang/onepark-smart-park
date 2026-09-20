-- ============================================================
-- OnePark M1 迁移脚本: device 表补 tenant_id/zone_id 列
-- 适用: 已按旧版 m1_mysql_tables.sql 建表的存量环境
-- 全新环境无需执行 —— 新版 m1_mysql_tables.sql 已包含这两列与索引.
-- 执行方式: Navicat 直接执行(MySQL 8.x)
-- ============================================================

ALTER TABLE `device`
  ADD COLUMN `tenant_id` BIGINT      NOT NULL DEFAULT 0  COMMENT '租户 ID(多园区隔离), 0 平台默认' AFTER `product_key`,
  ADD COLUMN `zone_id`   VARCHAR(64) NOT NULL DEFAULT '' COMMENT '能源区域编码(M4 计费/分析维度), 空未分区' AFTER `tenant_id`,
  ADD KEY `idx_tenant` (`tenant_id`),
  ADD KEY `idx_zone` (`zone_id`);

-- 存量设备回填示例(按产品归类):
-- UPDATE `device` SET `zone_id` = 'zone-a', `tenant_id` = 1 WHERE `product_key` = 'pk_electric_meter';
