-- ============================================================
-- L2 RBAC 数据隔离 - tenant_id 列迁移脚本
-- 配套: deploy/sql/rbac_data_scope.sql (sys_role.data_scope 已备)
-- 执行顺序: 先执行 rbac_data_scope.sql, 再执行本脚本
-- 变更性质: 仅加列 + 索引, 不删不改既有字段, 风险可控
-- ============================================================
--
-- 【执行前必读 / 待确认项】
-- 1) 库名: 下表用完全限定 <db>.<table>, 库划分对齐 init.sql。
--    M1 设备域(m1_mysql_tables.sql 注释指向 onepark-smart-park 一个库),
--    若 device-service 实际 DataSource 连 device_db/shadow_db, 请改为对应库名。
-- 2) 历史数据: ALTER 后 tenant_id 默认 0, 需按业务映射回填(见文末),
--    回填完成前 datascope 默认 ScopeTenant 会过滤真实数据, 详见方案文档。
-- 3) 主数据/全局配置表(不隔离, 不加列):
--    sys_menu / sys_role / sys_user_role / sys_role_menu / product / lease_zone / parking_fee_rule
--    (datascope 为 opt-in, 这些表的查询不调用 .Scopes 即视为"全部可见")
-- 4) 已具备 tenant_id 的表(无需本脚本): alarm / alarm_rule / alarm_operate_log / lease_contract / lease_bill
--
-- ============================================================

-- ---- sys_db: 用户归属园区(tenant_id 缺失是 x-tenant-id 恒为 0 的根因) ----
ALTER TABLE sys_db.sys_user
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '所属园区/租户ID, RBAC 数据隔离维度(用户归属园区)',
  ADD KEY idx_tenant (tenant_id);

-- ---- workorder_db ----
ALTER TABLE workorder_db.work_order
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE workorder_db.work_order_flow
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE workorder_db.work_order_attachment
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);

-- ---- visitor_db ----
ALTER TABLE visitor_db.visitor_record
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);

-- ---- parking_db ----
ALTER TABLE parking_db.parking_record
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
-- parking_fee_rule 为计费规则主数据, 默认不隔离(见上方待确认项 3); 若需按园区差异化再加列

-- ---- notice_db ----
ALTER TABLE notice_db.notice
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE notice_db.notice_read
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);

-- ---- M1 设备域(库名以 device-service 实际 DataSource 为准, 注释指向 onepark-smart-park) ----
-- device 现状用 park_id VARCHAR(32) 标识园区, 与 BIGINT 型 tenant_id 异构;
-- 二者并存, datascope 以 tenant_id 为准, park_id 保留兼容既有逻辑。
ALTER TABLE onepark-smart-park.device
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度(与 park_id 并存)',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE onepark-smart-park.command_log
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE onepark-smart-park.shadow
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
-- product 为产品模板, 全局共享, 不隔离(见待确认项 3)

-- ---- M5 招商/调度(已具备 lease_contract/lease_bill, 补齐其余业务表) ----
ALTER TABLE leasing_db.lease_contract_status_log
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
-- lease_zone 为全局区域主数据, 不隔离(见待确认项 3)
ALTER TABLE dispatch_db.dispatch_task
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);
ALTER TABLE dispatch_db.dispatch_task_log
  ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 0 COMMENT '园区ID, RBAC 行级隔离维度',
  ADD KEY idx_tenant (tenant_id);

-- ============================================================
-- 历史数据回填说明(执行 ALTER 后 tenant_id 默认 0):
--   * device: 可由 park_id 映射
--       UPDATE onepark-smart-park.device SET tenant_id = <园区ID> WHERE park_id = '<parkCode>';
--   * work_order / visitor_record / parking_record / notice 等若建单时未记录租户,
--       需业务侧补录或统一归到默认园区(DefaultTenantId=1)。
--   * sys_user: 按用户实际所属园区回填 tenant_id, 否则 JWT 注入 tenant_id=0。
-- 回填完成前, datascope 默认 ScopeTenant 会过滤 tenant_id=0 的真实数据;
-- 故回填应与 logic 接入(步骤2)配套上线, 或上线初期将默认 data_scope 暂设 1(全部)过渡。
-- ============================================================
