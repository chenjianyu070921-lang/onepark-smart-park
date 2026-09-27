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

-- ---------- 2026-09-24 历史「无租户告警」(tenant_id=0) 的处理方案 ----------
-- 背景: 2026-09-24 起消费链路默认不再落 tenant_id=0 的告警(Tenant.MissingPolicy=dlq),
--       但**此前已经落库的历史数据仍然是 0**。这些告警在任何按租户过滤的视图
--       (后台列表 / 大屏聚合 / WS 推送)里都看不见, 属于"产生了却没人看得到"的存量。
--
-- 处置前提: alarm-service 不维护设备主数据, 且约定禁止跨库 JOIN,
--       因此**设备 -> 租户的映射必须由 M1 提供**(device_db.device 表), 本脚本不臆造归属。
--
-- 两个可选动作, 按现场情况二选一(都不需要停机):
--   A. 回填(推荐): 由 M1 导出 device_id -> tenant_id 映射, 按下面的模板批量 UPDATE。
--   B. 归档: 确认无需保留的历史脏数据, 直接删除(见文件末尾的注释化 DELETE)。
--
-- 先建一个可随时查询的待回填视图(幂等, 无副作用, 两种动作都能用它核对进度):
CREATE OR REPLACE VIEW `v_alarm_orphan_tenant` AS
SELECT `id`, `alarm_no`, `device_id`, `event_type`, `level`, `status`, `created_at`
  FROM `alarm`
 WHERE `tenant_id` = 0;

-- 核对当前待回填量(回填/归档后应为 0):
--   SELECT COUNT(*) AS orphan_count FROM `v_alarm_orphan_tenant`;

-- === 动作 A 回填模板(把映射落到临时表后执行, 幂等且可重复执行) ===
-- CREATE TEMPORARY TABLE `tmp_device_tenant` (
--   `device_id` VARCHAR(64) NOT NULL PRIMARY KEY,
--   `tenant_id` BIGINT      NOT NULL
-- );
-- -- 由 M1 提供的映射逐行灌入(示例):
-- -- INSERT IGNORE INTO `tmp_device_tenant` VALUES ('door-01', 1), ('cam-02', 1);
-- UPDATE `alarm` a
--   JOIN `tmp_device_tenant` t ON t.`device_id` = a.`device_id`
--    SET a.`tenant_id` = t.`tenant_id`
--  WHERE a.`tenant_id` = 0;

-- === 动作 B 归档模板(确认无需保留时) ===
-- DELETE FROM `alarm` WHERE `tenant_id` = 0;

-- 说明: 因"缺 tenant_id"被拒收而进入 alarm_dlq 的消息, 不需要本脚本处理 ——
--       M1 补齐上报后, 用 POST /api/alarm/dlq/:id/replay 重放即可补回告警,
--       重放走的是原始报文, 补齐后的报文会带真实租户。
