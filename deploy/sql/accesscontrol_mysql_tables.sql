-- =====================================================================
-- OnePark 智慧园区 - access-control-service 门禁服务建表 SQL (成员3 / 2026-09-17)
-- P0 任务: 门禁基础逻辑 (组长安排, 由 M2 成员交付)
-- 覆盖 2 张表: 门禁点位 parking_gate / 通行记录 access_record
-- 字段与 app/access-control-service/internal/model/access.go 的 GORM 模型逐列对齐.
-- 目标库: 由执行时连接的 DSN 决定(当前部署为单一共享库 onepark-smart-park,
-- 表名全局唯一, 单库共存无冲突), 脚本内不含 CREATE DATABASE / USE.
-- 说明:
--   * 业务表统一携带 tenant_id (RBAC 行级隔离), 网关注入 x-tenant-id.
--   * 不建物理外键, gate_id 仅逻辑关联 parking_gate.id.
--   * 全部 CREATE TABLE IF NOT EXISTS: 幂等可重复执行, 已有同名表不会被改动.
-- =====================================================================

-- ---------------------------------------------------------------------
-- 1. 门禁/道闸点位表: 园区内可控门禁点位与 M1 设备(device_id)的映射.
--    远程开门按点位查到 device_id, 经 M1 SendCommand 下发指令.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `parking_gate` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT        NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  name        VARCHAR(64)   NOT NULL COMMENT '点位名称, 如 东门道闸/1号楼单元门',
  location    VARCHAR(128)           DEFAULT '' COMMENT '安装位置',
  device_id   VARCHAR(64)   NOT NULL COMMENT 'M1 设备中心注册的 device_id, SendCommand 下发目标',
  command     VARCHAR(32)   NOT NULL DEFAULT 'open_door' COMMENT '开门指令名',
  status      TINYINT       NOT NULL DEFAULT 1 COMMENT '1启用 0停用',
  created_at  DATETIME      NOT NULL COMMENT '创建时间',
  updated_at  DATETIME      NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_device` (device_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='门禁/道闸点位表';

-- ---------------------------------------------------------------------
-- 2. 通行记录表: 每次远程开门一条(成功/失败均落), 审计可溯.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `access_record` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT        NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  gate_id     BIGINT        NOT NULL COMMENT '门禁点位ID(parking_gate.id)',
  device_id   VARCHAR(64)            DEFAULT '' COMMENT '实际执行指令的 M1 设备ID',
  action      VARCHAR(32)   NOT NULL COMMENT '动作: remote_open 远程开门(预留 qr_pass 扫码通行)',
  result      TINYINT       NOT NULL COMMENT '1成功 0失败',
  operator_id BIGINT        NOT NULL DEFAULT 0 COMMENT '操作人(user_id), 0=系统',
  remark      VARCHAR(255)           DEFAULT '' COMMENT '开门事由/失败原因',
  created_at  DATETIME      NOT NULL COMMENT '创建时间',
  PRIMARY KEY (id),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_gate_created` (gate_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='门禁通行/远程开门记录表';
