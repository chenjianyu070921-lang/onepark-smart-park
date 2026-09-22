-- ============================================================================
-- M3 增量迁移(**仅用于已经建过库的环境**)
--
-- ⚠️ 重要: 本文件的 ALTER **不幂等**, 重复执行会报 1060 Duplicate column name 并中断,
--    导致后面的语句不再执行。执行前请先确认哪些列已经存在。
--
-- 全新环境不需要本文件 —— 直接执行 m3_mysql_tables.sql 即可, 那里已含全部列。
--
-- 应用:
--   docker cp deploy/sql/m3_mysql_migrations.sql onepark-mysql:/tmp/m3m.sql
--   docker exec onepark-mysql sh -c "mysql -uroot -p<密码> < /tmp/m3m.sql"
--   (若不确定是否已执行过, 加 --force 忽略 1060: ... mysql --force -uroot -p<密码> < /tmp/m3m.sql)
--
-- 已知列 -> 来源变更:
--   alarm_rule.device_type    告警规则按「设备类型 + 事件类型」映射告警等级
-- ============================================================================

USE `alarm_db`;

-- ---------- 2026-09-20 告警规则: 设备类型维度 ----------
-- 新增列默认 '' = 不限设备类型, 与规则引擎 AppliesTo 的"空值不限"语义一致,
-- 因此存量规则升级后行为不变(不会因为加列而突然漏报)。
ALTER TABLE `alarm_rule`
  ADD COLUMN `device_type` VARCHAR(32) NOT NULL DEFAULT '' COMMENT '设备类型(access_control/camera/sensor...), 为空表示不限设备类型' AFTER `name`,
  ADD KEY `idx_device_type` (`device_type`);

-- 把平台级门禁闯入种子规则(固定主键 1 / 3)补上设备类型, 与 m3_mysql_tables.sql 的种子保持一致。
-- 只改这两条 id: 业务自建规则的 device_type 由运营在后台自行维护, 迁移不做批量猜测。
UPDATE `alarm_rule`
   SET `device_type` = 'access_control'
 WHERE `id` IN (1, 3)
   AND `tenant_id` = 0
   AND `device_type` = '';

-- ---------- 2026-09-22 门禁闯入告警等级: 一般(2) -> 严重(3) ----------
-- 口径: 单次门禁非法闯入即"高"级告警(level=3), 与"短时反复闯入"(规则③, level=3)同档。
--
-- 为什么要独立一条 UPDATE: m3_mysql_tables.sql 的种子是 INSERT IGNORE,
-- 已建库的环境执行建表脚本不会更新存量行, 必须靠本迁移把等级抬上去。
-- 条件带 `level` = 2 使其**幂等**(第二次执行 0 行), 且不会覆盖运维手工调整过的等级。
UPDATE `alarm_rule`
   SET `level` = 3
 WHERE `id` = 1
   AND `tenant_id` = 0
   AND `level` = 2;
