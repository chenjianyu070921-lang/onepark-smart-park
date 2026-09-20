-- =====================================================================
-- M2 P2 功能 - 公告撤回 (notice recall) 迁移评审稿
-- 状态: 评审稿(未执行), 仅供组长/DBA 评审, 不进入 CI 自动执行.
-- 对齐: docs/M2物业管理服务模块设计文档.md 第八章 状态机 + 公告域模型
--       (app/notice-service/internal/model).
-- ---------------------------------------------------------------------
-- 关键事实(对齐现有建表 m2_mysql_tables.sql):
--   * notice.status 已定义 1草稿 2已发布 3已撤回(m2_mysql_tables.sql L165),
--     撤回主路径仅需将 status 由 2(已发布) -> 3(已撤回), 纯逻辑即可闭环,
--     不新增任何列也能工作(最小化实现).
--   * 本脚本补充审计字段 recalled_at / recall_reason, 用于撤回溯源与
--     补偿事件(notice-event 下架通知)对账; 若走最小化方案, 可跳过本 ALTER,
--     仅逻辑层将 status 置 3.
--   * 业务约束: 撤回仅对 已发布(2) 生效; 草稿(1)/定时未发布(publish_at>now)
--     不允许撤回(逻辑层校验, 非本脚本职责).
--   * 撤回后应发 Kafka notice-event(compensation) 通知前端下架, 属逻辑层.
-- =====================================================================

-- 撤回审计字段(可选, 最小化实现可注释掉)
ALTER TABLE `notice`
  ADD COLUMN `recalled_at`   DATETIME     DEFAULT NULL COMMENT '撤回时间(NULL=未撤回)' AFTER `status`,
  ADD COLUMN `recall_reason` VARCHAR(512) DEFAULT NULL COMMENT '撤回原因'              AFTER `recalled_at`;

-- 撤回查询索引: 按 (租户, 状态, 撤回时间) 快速捞出待下架/已撤回公告
ALTER TABLE `notice`
  ADD KEY `idx_tenant_status_recalled` (`tenant_id`, `status`, `recalled_at`);
