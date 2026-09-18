-- ============================================================
-- OnePark M3 门禁 - MySQL 业务表
-- 适用数据库：access_db（init.sql 已预创建该库）
-- 执行方式：Navicat 直接执行 / 容器初始化脚本
-- 编码：UTF-8 无 BOM（中文注释在 GBK 连接下会被误解析，执行前请 SET NAMES utf8mb4）
-- 约定：不建物理外键，仅逻辑关联；tenant_id 为 RBAC 行级隔离维度
-- ============================================================
SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;

USE `access_db`;

-- ############################################################
-- access_operate_log 开门操作审计(docs/m3/04 #47 / §2.2)
-- 说明: 每次远程开门无论成功/失败/超时都要留痕; 表中 result 取值
--       1 成功 / 0 失败 / 2 超时, 与 model.CommandResultXXX 一致.
--       相比文档额外补两列: tenant_id(RBAC 行级隔离, 与全仓约定一致)
--       与 reason(操作缘由如"访客放行", 审计可追溯).
-- ############################################################
CREATE TABLE IF NOT EXISTS `access_operate_log` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '门禁设备ID(来自 M1, 不维护设备主数据)',
  `operator_id` BIGINT       NOT NULL DEFAULT 0  COMMENT '操作人(user_id), 远程开门必填',
  `command`     VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '下发命令: open_door',
  `result`      TINYINT      NOT NULL DEFAULT 0  COMMENT '结果: 1成功 0失败 2超时',
  `message`     VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'M1/设备返回信息或失败原因',
  `reason`      VARCHAR(255) NOT NULL DEFAULT '' COMMENT '开门缘由(请求携带, 可为空)',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '操作时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_device` (`device_id`),
  KEY `idx_operator` (`operator_id`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='开门操作审计表';

-- ############################################################
-- access_permission 门禁权限(docs/m3/04 #45/#46 / §2.2)
-- 说明: uk_person_device 文档约定为 (person_id, device_id), 本实现前置 tenant_id
--       改为 (tenant_id, person_id, device_id), 保证多园区同 person_id 不互相串=(RBAC 隔离).
-- ############################################################
CREATE TABLE IF NOT EXISTS `access_permission` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `person_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '人员ID',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '门禁设备ID',
  `time_window` JSON         NULL                 COMMENT '时间段权限 {start,end,days}, 空表示全天',
  `whitelist`   TINYINT      NOT NULL DEFAULT 0  COMMENT '白名单(跳过时间段校验) 1是/0否',
  `status`      TINYINT      NOT NULL DEFAULT 1  COMMENT '1有效 0失效',
  `expire_at`   DATETIME     NULL DEFAULT NULL   COMMENT '过期时间, NULL表示长期有效',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_person_device` (`tenant_id`, `person_id`, `device_id`),
  KEY `idx_person` (`person_id`),
  KEY `idx_device` (`device_id`),
  KEY `idx_status` (`status`),
  KEY `idx_expire` (`expire_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='门禁权限表';

-- ############################################################
-- access_record 通行记录(docs/m3/04 #48 / §2.2)
-- 说明: 由门禁设备上行事件写入(M1 Kafka 事件 → 本表), open_type 区分
--       card(刷卡)/face(人脸)/remote(远程开门)/qrcode(访客码) 四种开闸方式.
-- ############################################################
CREATE TABLE IF NOT EXISTS `access_record` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `tenant_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '园区ID, RBAC 数据隔离维度',
  `person_id`   BIGINT       NOT NULL DEFAULT 0  COMMENT '人员ID',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '门禁设备ID',
  `result`      TINYINT      NOT NULL DEFAULT 1  COMMENT '1成功/0失败(见 §5.4)',
  `open_type`   VARCHAR(16)  NOT NULL DEFAULT '' COMMENT 'card/face/remote/qrcode',
  `fail_reason` VARCHAR(128) NOT NULL DEFAULT '' COMMENT '失败原因',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '通行时间',
  PRIMARY KEY (`id`),
  KEY `idx_tenant` (`tenant_id`),
  KEY `idx_person` (`person_id`),
  KEY `idx_device` (`device_id`),
  KEY `idx_result` (`result`),
  KEY `idx_created` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='通行记录表';
