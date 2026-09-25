-- =====================================================================
-- M2 P2 功能 - 访客多方式核验 (visitor multi-channel verify) 落地迁移
-- 状态: 正式迁移(幂等, 可重复执行), 已接入 run-all.sh.
-- 对齐: docs/M2物业管理服务模块设计文档.md §5.2 门岗验证「二维码/手机号/身份证」
--       model.VisitorRecord(id_no / verify_channel / face_token / badge_no).
-- ---------------------------------------------------------------------
-- 背景: 多渠道核验代码已就绪(邀请落库凭证列 + 签入按 verify_channel 选核验链路),
--       但本脚本此前为"评审稿未执行", 新环境 run-all.sh 初始化后 visitor_record 无
--       这些列, 签入按 id_no/face_token 查询报 1054 未知列, 多渠道核验形同虚设.
--       现改为幂等加列迁移(列存在性守卫), 接入 run-all 后新环境初始化即具备核验能力.
-- 约束: 仅新增列与索引, 不改动既有列/数据, 不污染现有业务数据(对齐项目硬规则).
-- 已知待办(评审稿原标注, 非本脚本阻塞项): id_no 当前为明文存储,
--       应用层加密方案(身份证号属敏感个人信息)待确认后再升级.
-- =====================================================================

USE `visitor_db`;

DELIMITER $$

-- add_visitor_verify_cols: 幂等加列 + 加索引, 可安全重复执行(列/索引存在则跳过).
-- 用存储过程包裹以便逐列做 information_schema 存在性守卫, 规避 MySQL
-- "ADD COLUMN IF NOT EXISTS" 语法不支持的问题.
CREATE PROCEDURE IF NOT EXISTS `add_visitor_verify_cols`()
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND COLUMN_NAME = 'id_no') THEN
    ALTER TABLE `visitor_record`
      ADD COLUMN `id_no` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '身份证号(加密存储, 核验方式=3)' AFTER `visitor_phone`;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND COLUMN_NAME = 'verify_channel') THEN
    ALTER TABLE `visitor_record`
      ADD COLUMN `verify_channel` TINYINT NOT NULL DEFAULT 1 COMMENT '核验方式 1二维码 2手机号 3身份证 4人脸 5工牌' AFTER `id_no`;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND COLUMN_NAME = 'face_token') THEN
    ALTER TABLE `visitor_record`
      ADD COLUMN `face_token` VARCHAR(256) NOT NULL DEFAULT '' COMMENT '人脸特征令牌(核验方式=4时非空)' AFTER `verify_channel`;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM information_schema.COLUMNS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND COLUMN_NAME = 'badge_no') THEN
    ALTER TABLE `visitor_record`
      ADD COLUMN `badge_no` VARCHAR(64) NOT NULL DEFAULT '' COMMENT '工牌号(核验方式=5时非空)' AFTER `face_token`;
  END IF;

  -- 多方式核验查询索引: 按 (租户, 核验方式, 手机号/身份证) 快速命中(对应签入 verifyChannelColumn 的查找).
  IF NOT EXISTS (SELECT 1 FROM information_schema.STATISTICS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND INDEX_NAME = 'idx_tenant_verify_phone') THEN
    ALTER TABLE `visitor_record` ADD KEY `idx_tenant_verify_phone` (`tenant_id`, `verify_channel`, `visitor_phone`);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM information_schema.STATISTICS
                 WHERE TABLE_SCHEMA = 'visitor_db' AND TABLE_NAME = 'visitor_record' AND INDEX_NAME = 'idx_tenant_verify_idno') THEN
    ALTER TABLE `visitor_record` ADD KEY `idx_tenant_verify_idno` (`tenant_id`, `verify_channel`, `id_no`);
  END IF;
END$$

DELIMITER ;

CALL `add_visitor_verify_cols`();
DROP PROCEDURE IF EXISTS `add_visitor_verify_cols`;
