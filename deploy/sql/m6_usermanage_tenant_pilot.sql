-- ============================================================
-- M6 user-manage 多租户试点迁移: sys_user 加 tenant_id + 存量回填
-- 目的: 验证 common/datascope 在 user-manage 真正闭环(查询接 .Scopes, 按 x-tenant-id 隔离)
-- 说明:
--   * sys_user 的 ALTER 亦已含于 deploy/sql/l2_tenant_id_migration.sql(跨模块 L2 迁移, DEFAULT 0);
--     本文件独立幂等, 便于仅运行 user-manage 试点时自包含执行(与 l2 重复执行不冲突, 加列有存在性守卫).
--   * RBAC 全局配置表(sys_menu/sys_role/sys_user_role/sys_role_menu)不隔离, 不加列(见 l2 迁移待确认项 3).
-- 依赖: sys_db 已存在(见 sys.sql)
-- 执行: 手动导入或并入 run-all.sh; 本文件仅加列+回填, 风险可控.
-- ============================================================

USE sys_db;

-- 1) 幂等加列(已存在则跳过)
SET @has_col = (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = 'sys_db' AND TABLE_NAME = 'sys_user' AND COLUMN_NAME = 'tenant_id'
);
SET @sql = IF(
    @has_col = 0,
    'ALTER TABLE sys_user ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 1 COMMENT ''所属园区/租户ID, RBAC 数据隔离维度(用户归属园区)''',
    'SELECT 1'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 2) 幂等补索引
SET @has_idx = (
    SELECT COUNT(*) FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = 'sys_db' AND TABLE_NAME = 'sys_user' AND INDEX_NAME = 'idx_sys_user_tenant'
);
SET @sql_idx = IF(
    @has_idx = 0,
    'ALTER TABLE sys_user ADD KEY idx_sys_user_tenant (tenant_id)',
    'SELECT 1'
);
PREPARE stmt2 FROM @sql_idx;
EXECUTE stmt2;
DEALLOCATE PREPARE stmt2;

-- 3) 存量数据回填: 试点阶段统一归到默认园区(DefaultTenantId=1),
--    使本地联调(ctx tenant_id=1)能查到既有用户(含种子 admin);
--    多园区真实映射由业务侧后续按实际园区补录.
--    注意: l2 迁移默认 0, 回填到 1 后 datascope 默认 ScopeTenant 才能命中本地数据.
UPDATE sys_user SET tenant_id = 1 WHERE tenant_id = 0;
