-- =====================================================================
-- M2 P2 功能 - 停车月卡表 (parking_db.monthly_card)
-- 状态: 正式迁移(幂等, CREATE TABLE IF NOT EXISTS, 可重复执行), 已接入 run-all.sh.
-- 对齐: app/parking-service/internal/model/monthlycard.go (MonthlyCard 模型字段一一对应).
-- ---------------------------------------------------------------------
-- 背景: 月卡管理(建卡/续期/停用/列表)与月卡车辆识别(入场免计费判定)此前只有 GORM
--       模型没有建表脚本, 新环境 run-all.sh 初始化后 monthly_card 不存在,
--       月卡接口直接报 1146 表不存在(看板 P0 卡点, 半天可修).
-- 约束: 仅新增表, 不改动任何既有表结构/字段/索引, 不污染现有业务数据(对齐项目硬规则).
-- 识别口径: 车辆入场时按 (tenant_id, plate_no) 查 status=1(生效)
--           且 start_time<=入场时刻<=end_time; 命中则该次停车按月卡计费(fee=0).
-- =====================================================================

USE `parking_db`;

CREATE TABLE IF NOT EXISTS `monthly_card` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `tenant_id`   BIGINT      NOT NULL DEFAULT 0 COMMENT '园区ID(RBAC 隔离)',
  `plate_no`    VARCHAR(32) NOT NULL COMMENT '车牌号',
  `owner_name`  VARCHAR(64) NOT NULL DEFAULT '' COMMENT '车主姓名',
  `phone`       VARCHAR(20) NOT NULL DEFAULT '' COMMENT '联系电话',
  `start_time`  DATETIME    NOT NULL COMMENT '生效起',
  `end_time`    DATETIME    NOT NULL COMMENT '生效止(到期时间)',
  `status`      TINYINT     NOT NULL DEFAULT 1 COMMENT '1生效 2停用',
  `created_at`  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  -- 月卡识别核心查询索引: (租户, 车牌), 服务 HasActiveMonthlyCard 的入场免计费判定.
  KEY `idx_tenant_plate` (`tenant_id`, `plate_no`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车月卡(生效期内车牌入场按月卡计费)';
