-- ============================================================================
-- M5 增量迁移（**仅用于已经建过库的环境**）
--
-- ⚠️ 重要: 本文件的 ALTER **不幂等**, 重复执行会报 1060 Duplicate column name 并中断,
--    导致后面的语句不再执行。执行前请先确认哪些列已经存在。
--
-- 全新环境不需要本文件 —— 直接执行 m5_mysql_tables.sql 即可, 那里已含全部列。
--
-- 应用:
--   docker cp deploy/sql/m5_mysql_migrations.sql onepark-mysql:/tmp/m5m.sql
--   docker exec onepark-mysql sh -c "mysql -uroot -p<密码> < /tmp/m5m.sql"
--   （若不确定是否已执行过, 加 --force 忽略 1060: ... mysql --force -uroot -p<密码> < /tmp/m5m.sql）
--
-- 已知列 -> 来源变更:
--   dispatch_task.required_skill    智能派单(技能优先)
--   dispatch_task.reassign_count    指派超时重派
--   lease_contract.auto_renew       合同到期自动续签
--   lease_contract.renew_notice_days 续签提醒窗口
-- ============================================================================

-- ---------- 2026-09-16 智能派单: 工单所需技能 ----------
ALTER TABLE `dispatch_db`.`dispatch_task`
  ADD COLUMN `required_skill` VARCHAR(32) NOT NULL DEFAULT '' COMMENT '所需技能标签, 空表示不限; 自动指派按技能优先' AFTER `zone_code`;

-- ---------- 2026-09-16 合同到期自动续签 ----------
-- 默认 0(不自动续约) —— 自动延长租期本质上是在替承租方做决定, 必须由合同条款显式约定。
ALTER TABLE `leasing_db`.`lease_contract`
  ADD COLUMN `auto_renew` TINYINT NOT NULL DEFAULT 0 COMMENT '1约定自动续约 0到期即止',
  ADD COLUMN `renew_notice_days` INT NOT NULL DEFAULT 30 COMMENT '到期前多少天进入续签提醒窗口';

-- ---------- 2026-09-17 指派超时重派: 重派次数计数 ----------
ALTER TABLE `dispatch_db`.`dispatch_task`
  ADD COLUMN `reassign_count` INT NOT NULL DEFAULT 0 COMMENT 'cron 自动重派次数; 达上限后释放为待指派交人工';
