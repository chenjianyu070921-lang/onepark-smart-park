-- ============================================================================
-- M5 存量数据租户回填（**只动 tenant_id = 0 的行，幂等，可重复执行**）
--
-- 【为什么需要】
--   平台从「单园区」过渡到「多园区 RBAC」时给业务表补了 tenant_id 列, 补列后**历史行默认 0**;
--   而 M5 的查询一律按 ctx 租户过滤(网关注入的 x-tenant-id, 见 common/ctxdata.GetTenantId)——
--   于是 tenant_id = 0 的历史行在页面上**查不到**, 看起来像"数据丢了"。
--   (该问题在 docs/plans/2026-09-20-M5未实现清单.md 里挂了很久: "遗留待确认: 存量数据的租户回填")
--
-- 【回填成什么】1
--   与配置里的 DefaultTenantId 一致(deploy/m5/etc/dispatch-api.yaml: DefaultTenantId: 1),
--   即当前唯一的演示/默认园区。**不要**按"看起来像哪个园区"去猜, 本平台本期只有一个园区。
--
-- 【只回填 0，不动别的】
--   库里存在测试遗留的非 0 租户(纳秒级, 形如 9000000100), 那是测试数据不是历史数据 ——
--   本脚本只动 = 0 的行, 不会碰它们; 清理测试数据是另一件事, **不要**在同一个脚本里顺手删。
--
-- 【不含这两张表】
--   lease_zone / dispatch_staff 没有 tenant_id 列(本期单园区, 见未实现清单的 A5 决策记录), 无需回填。
--
-- 【怎么执行】
--   docker cp deploy/sql/m5_tenant_backfill.sql onepark-mysql:/tmp/m5bf.sql
--   docker exec onepark-mysql sh -c "mysql -uroot -p<密码> < /tmp/m5bf.sql"
--   ⚠️ 生产库上请先跑 STEP 1 看计数, 再决定是否整批执行; 大表建议分批(见 STEP 2 注释)。
--
-- 【回滚】
--   本脚本是纯 DML 且只改 tenant_id, 回滚等价于把同一批再置回 0 ——
--   但**必须先把受影响范围记下来**(STEP 1 的计数 + 对应主键区间), 否则无法精确回滚。
--   要求严格可回滚时, 执行前先备份:
--     CREATE TABLE lease_contract_bak_20260927 AS SELECT id, tenant_id FROM lease_contract;
-- ============================================================================

-- ---------- STEP 1. 回填前计数(请先看这份数字) ----------
SELECT 'lease_contract'            AS tbl, COUNT(*) AS zero_rows FROM leasing_db.lease_contract            WHERE tenant_id = 0
UNION ALL
SELECT 'lease_bill',                       COUNT(*)           FROM leasing_db.lease_bill                WHERE tenant_id = 0
UNION ALL
SELECT 'lease_contract_status_log',        COUNT(*)           FROM leasing_db.lease_contract_status_log WHERE tenant_id = 0
UNION ALL
SELECT 'dispatch_task',                    COUNT(*)           FROM dispatch_db.dispatch_task            WHERE tenant_id = 0
UNION ALL
SELECT 'dispatch_task_log',                COUNT(*)           FROM dispatch_db.dispatch_task_log        WHERE tenant_id = 0;

-- ---------- STEP 2. 回填(只动 = 0 的行, 可重复执行) ----------
-- 大表上建议加 LIMIT 分批执行并循环, 例如:
--   UPDATE lease_bill SET tenant_id = 1 WHERE tenant_id = 0 ORDER BY id LIMIT 5000;   -- 反复执行直到 0 rows affected
UPDATE leasing_db.lease_contract            SET tenant_id = 1 WHERE tenant_id = 0;
UPDATE leasing_db.lease_bill                SET tenant_id = 1 WHERE tenant_id = 0;
UPDATE leasing_db.lease_contract_status_log SET tenant_id = 1 WHERE tenant_id = 0;
UPDATE dispatch_db.dispatch_task            SET tenant_id = 1 WHERE tenant_id = 0;
UPDATE dispatch_db.dispatch_task_log        SET tenant_id = 1 WHERE tenant_id = 0;

-- ---------- STEP 3. 回填后校验 ----------
-- 3.1 应该全部为 0 行(若有残留, 说明有并发写入或漏了表)
SELECT 'lease_contract'            AS tbl, COUNT(*) AS still_zero FROM leasing_db.lease_contract            WHERE tenant_id = 0
UNION ALL
SELECT 'lease_bill',                       COUNT(*)            FROM leasing_db.lease_bill                WHERE tenant_id = 0
UNION ALL
SELECT 'lease_contract_status_log',        COUNT(*)            FROM leasing_db.lease_contract_status_log WHERE tenant_id = 0
UNION ALL
SELECT 'dispatch_task',                    COUNT(*)            FROM dispatch_db.dispatch_task            WHERE tenant_id = 0
UNION ALL
SELECT 'dispatch_task_log',                COUNT(*)            FROM dispatch_db.dispatch_task_log        WHERE tenant_id = 0;

-- 3.2 用**业务查询的形状**验证一遍(这才是真正要证明的: 老数据能被应用查到了)。
--     带 x-tenant-id: 1 的请求, 走的就是下面这两条 SQL 的口径。
SELECT COUNT(*) AS contract_visible_for_tenant1 FROM leasing_db.lease_contract WHERE tenant_id = 1;
SELECT COUNT(*) AS task_visible_for_tenant1     FROM dispatch_db.dispatch_task WHERE tenant_id = 1;

-- 3.3 抽查一条历史工单的审计时间线是否连续(建单那一格的 from_status 应为 0)
SELECT task_id, from_status, to_status, action, tenant_id
FROM dispatch_db.dispatch_task_log
ORDER BY id DESC
LIMIT 10;
