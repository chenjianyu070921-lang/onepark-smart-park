-- =====================================================================
-- M2 P2 功能 - 访客多方式核验 (visitor multi-channel verify) 迁移评审稿
-- 状态: 评审稿(未执行), 仅供组长/DBA 评审, 不进入 CI 自动执行.
-- 对齐: docs/M2物业管理服务模块设计文档.md §5.2 访客管理「门岗验证: 二维码/手机号/身份证」
--       (app/visitor-service/internal/model/visitorrecord.go).
-- ---------------------------------------------------------------------
-- 设计取舍:
--   * 现有 visitor_record 仅含 visitor_name / visitor_phone / qr_code, 核验入口单一(二维码).
--     本脚本在 visitor_record 上扩展核验维度字段, 支持 身份证 / 人脸 / 工牌 等多方式.
--   * verify_channel 枚举: 1二维码 2手机号 3身份证 4人脸 5工牌; 签入时按该字段选校验链路.
--   * id_no(身份证号) / face_token(人脸特征令牌) 属敏感生物/身份信息, 建议加密存储,
--     库内仅存密文或令牌引用; 本脚本为明文占位, 落地前需确认加密方案.
--   * 备选方案: 若不愿加宽 visitor_record, 可改为独立 visitor_verify 表(visitor_id,
--     channel, credential, verified_at); 本脚本采用加列方案, 与现有模型改动最小.
-- =====================================================================

ALTER TABLE `visitor_record`
  ADD COLUMN `id_no`         VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '身份证号(加密存储, 核验方式=3)' AFTER `visitor_phone`,
  ADD COLUMN `verify_channel` TINYINT      NOT NULL DEFAULT 1 COMMENT '核验方式 1二维码 2手机号 3身份证 4人脸 5工牌' AFTER `id_no`,
  ADD COLUMN `face_token`    VARCHAR(256) NOT NULL DEFAULT '' COMMENT '人脸特征令牌(核验方式=4时非空)' AFTER `verify_channel`,
  ADD COLUMN `badge_no`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '工牌号(核验方式=5时非空)' AFTER `face_token`;

-- 多方式核验查询索引: 按 (租户, 核验方式, 手机号/身份证) 快速命中
ALTER TABLE `visitor_record`
  ADD KEY `idx_tenant_verify_phone` (`tenant_id`, `verify_channel`, `visitor_phone`),
  ADD KEY `idx_tenant_verify_idno`  (`tenant_id`, `verify_channel`, `id_no`);
