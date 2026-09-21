-- =====================================================================
-- M2 P2 功能 - 访客黑名单 (visitor blocklist) 迁移评审稿
-- 状态: 评审稿(未执行), 仅供组长/DBA 评审, 不进入 CI 自动执行.
-- 对齐: docs/M2物业管理服务模块设计文档.md §5.2 访客管理「黑名单: 拉黑、自动禁止预约」
--       (app/visitor-service/internal/model).
-- ---------------------------------------------------------------------
-- 设计取舍:
--   * 现有 visitor_record 已有 blacklisted TINYINT 字段(仅标记"本条记录是否黑名单"),
--     但无法阻止"被拉黑人员用新邀请再次入园". 故本脚本新建独立 visitor_blocklist
--     表, 以 phone / id_no 为拉黑维度, 邀请/签入时实时查表拦截(核验方式之一).
--   * id_no(身份证号) 属敏感个人信息, 建议加密存储(应用层 AES), 库内仅存密文;
--     本脚本字段为明文占位, 落地前需确认加密方案.
--   * 与 visitor_record.blacklisted 的关系: 签入命中 blocklist 时, 同步将对应
--     visitor_record.blacklisted 置 1, 保持双写一致(逻辑层职责, 非本脚本).
-- =====================================================================

CREATE TABLE IF NOT EXISTS `visitor_blocklist` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  visitor_name  VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '访客姓名(可选, 便于人工核对)',
  phone         VARCHAR(20)  NOT NULL DEFAULT '' COMMENT '手机号(拉黑维度之一)',
  id_no         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '身份证号(加密存储, 拉黑维度之一)',
  reason        VARCHAR(512)          DEFAULT NULL COMMENT '拉黑原因',
  effective_from DATETIME             DEFAULT NULL COMMENT '生效起(NULL=立即)',
  effective_to   DATETIME             DEFAULT NULL COMMENT '生效止(NULL=永久)',
  status        TINYINT     NOT NULL DEFAULT 1 COMMENT '1生效 2解除',
  operator_id   BIGINT       NOT NULL DEFAULT 0 COMMENT '操作人(user_id)',
  created_at    DATETIME     NOT NULL COMMENT '创建时间',
  updated_at    DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_tenant_phone`  (tenant_id, phone),
  KEY `idx_tenant_idno`   (tenant_id, id_no),
  KEY `idx_tenant_status` (tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='访客黑名单(按手机号/身份证拉黑, 邀请签入实时拦截)';
