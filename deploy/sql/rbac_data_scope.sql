-- RBAC 数据权限范围迁移脚本
-- 数据范围级别: 1=全部 2=本园区/租户 3=本部门 4=本人 5=自定义
-- 注意: 数据库变更需 DBA/负责人确认后执行, 本文件为脚本, 不自动执行.
ALTER TABLE sys_role ADD COLUMN data_scope TINYINT NOT NULL DEFAULT 2 COMMENT '数据权限范围(1全部 2本园区/租户 3本部门 4本人 5自定义)';
