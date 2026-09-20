-- RBAC 数据权限范围字段补列脚本(存量库手动执行兜底)
-- 说明: data_scope 列已并入 deploy/sql/sys.sql 的 sys_role 建表语句(随 mysql 容器首次初始化自动执行).
--       本脚本仅用于「已按旧 schema 初始化的存量库」手动补齐列, 且做了存在性守卫, 重复执行不会报错.
-- 数据范围级别: 1=全部 2=本园区/租户 3=本部门 4=本人 5=自定义

SET @has_col = (
    SELECT COUNT(*) FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = 'sys_db' AND TABLE_NAME = 'sys_role' AND COLUMN_NAME = 'data_scope'
);
SET @sql = IF(
    @has_col = 0,
    'ALTER TABLE sys_role ADD COLUMN data_scope TINYINT NOT NULL DEFAULT 2 COMMENT ''数据权限范围(1全部 2本园区/租户 3本部门 4本人 5自定义)''',
    'SELECT 1'
);
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
