-- =====================================================================
-- OnePark 智慧园区 - M2 物业管理服务建表 SQL (成员3 / 2026-09-16)
-- 覆盖 11 张表, 按业务域分 5 组 (逻辑分组, 禁止跨域 JOIN):
--   工单域 : work_order / work_order_flow / work_order_attachment
--   计费域 : billing_rule / bill / energy_reading
--   公告域 : notice / notice_read
--   停车域 : parking_record / parking_fee_rule
--   访客域 : visitor_record
-- 说明(2026-09-17 增补): 初版遗漏停车域与访客域建表, 导致 parking/visitor 服务落库报
--   Error 1146(Table '...' doesn't exist); 此处补齐, 字段与各服务 internal/model 逐列对齐.
-- 字段与各服务 internal/model 下的 GORM 模型逐列对齐, 并补充查询索引.
-- 目标库: 由执行时连接的 DSN 决定(当前部署为单一共享库 onepark-smart-park,
-- 8 张表名全局唯一, 单库共存无冲突), 脚本内不含 CREATE DATABASE / USE.
-- 说明:
--   * 所有业务表统一携带 tenant_id (RBAC 行级隔离), 网关注入 x-tenant-id.
--   * 金额统一 DECIMAL; energy_reading.usage_kwh 与 Go 模型一致保持 DOUBLE.
--   * work_order.alarm_id / notice.source 为可空唯一键, 用于消息幂等:
--     MySQL 唯一索引允许多个 NULL, 人工数据(为 NULL)互不冲突.
--   * 全部 CREATE TABLE IF NOT EXISTS: 幂等可重复执行, 已有同名表不会被改动.
--
-- 统一执行顺序说明(部署/评审对齐口径, 2026-09-18):
--   1. init.sql            (预创建库与账号)
--   2. m1_mysql_tables.sql / m1_tdengine_tables.sql
--   3. m2_mysql_tables.sql (本文件)
--   4. m3_mysql_tables.sql / m3_access_mysql_tables.sql / m3_video_mysql_tables.sql
--   5. m5_mysql_tables.sql
--   说明: 当前共享部署为单一库 onepark-smart-park, 各脚本头部的 `USE xxx_db` 切库语句
--         在单库部署下应跳过(或按目标 DSN 落库); 脚本均幂等, 顺序执行互不依赖.
-- =====================================================================

-- ---------------------------------------------------------------------
-- 1. 工单域 (对齐 app/workorder-service/internal/model)
-- ---------------------------------------------------------------------

-- 工单主表: 状态机 0待派单 1处理中 2待验收 3已完成 4已关闭; version 乐观锁.
CREATE TABLE IF NOT EXISTS `work_order` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  order_no      VARCHAR(32)  NOT NULL COMMENT '工单号 WO-YYYYMMDD-XXXX',
  type          TINYINT      NOT NULL COMMENT '1报修 2投诉 3巡检 4保洁 5装修 6搬运 7其他',
  title         VARCHAR(128) NOT NULL COMMENT '工单标题',
  description   VARCHAR(1024)          DEFAULT NULL COMMENT '工单描述',
  reporter_id   BIGINT       NOT NULL COMMENT '报修/发起人(user_id)',
  assignee_id   BIGINT       NOT NULL DEFAULT 0 COMMENT '处理人, 未派单为0',
  department_id BIGINT       NOT NULL DEFAULT 0 COMMENT '处理部门',
  status        TINYINT      NOT NULL DEFAULT 0 COMMENT '0待派单 1处理中 2待验收 3已完成 4已关闭',
  priority      TINYINT      NOT NULL DEFAULT 2 COMMENT '1紧急 2普通 3低',
  location      VARCHAR(128)           DEFAULT NULL COMMENT '位置',
  attachments   VARCHAR(1024)          DEFAULT NULL COMMENT 'MinIO 对象Key列表(JSON)',
  version       BIGINT       NOT NULL DEFAULT 0 COMMENT '乐观锁版本',
  finished_at   DATETIME              DEFAULT NULL COMMENT '完成/关闭时间',
  alarm_id      VARCHAR(64)           DEFAULT NULL COMMENT '来源告警ID(告警自动建单时非空), 幂等键',
  created_at    DATETIME     NOT NULL COMMENT '创建时间',
  updated_at    DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_order_no` (order_no),
  UNIQUE KEY `uk_alarm_id` (alarm_id),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_status` (status),
  KEY `idx_tenant_status_assignee` (tenant_id, status, assignee_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单主表';

-- 工单操作流水: 每次流转(建单/派单/提交/验收/驳回/关闭)一条, 用于审计.
CREATE TABLE IF NOT EXISTS `work_order_flow` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT      NOT NULL COMMENT '园区ID',
  work_order_id BIGINT      NOT NULL COMMENT '关联工单ID',
  from_status   TINYINT              DEFAULT NULL COMMENT '流转前状态(-1 表示建单)',
  to_status     TINYINT     NOT NULL COMMENT '流转后状态',
  action        VARCHAR(32) NOT NULL COMMENT '动作: create/assign/submit/approve/reject/close',
  operator_id   BIGINT      NOT NULL COMMENT '操作人(user_id)',
  remark        VARCHAR(512)         DEFAULT NULL COMMENT '备注',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_wo` (work_order_id),
  KEY `idx_tenant` (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单操作流水';

-- 工单附件映射: 数据库仅存 MinIO 对象Key, 文件实体在 MinIO.
CREATE TABLE IF NOT EXISTS `work_order_attachment` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT      NOT NULL COMMENT '园区ID',
  work_order_id BIGINT      NOT NULL COMMENT '关联工单ID',
  object_key    VARCHAR(256) NOT NULL COMMENT 'MinIO 对象Key',
  file_name     VARCHAR(256)          DEFAULT NULL COMMENT '原始文件名',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_wo` (work_order_id),
  KEY `idx_tenant` (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='工单附件(MinIO 对象映射)';

-- ---------------------------------------------------------------------
-- 2. 计费域 (对齐 app/billing-service/internal/model)
--    注意: 主键/租户为 BIGINT UNSIGNED, 与模型 uint64 对齐.
--    ⚠️ energy_reading 的写入方是 M4 energy-data-service,
--      其写入侧尚未填 tenant_id, 现阶段以 zone_id 作为隔离维度.
-- ---------------------------------------------------------------------

-- 计费规则: 一条规则 = 某区域(zone_id, 空串为全园区默认)一度电怎么算钱.
CREATE TABLE IF NOT EXISTS `billing_rule` (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id  BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(0=历史数据/M4侧)',
  name       VARCHAR(64) NOT NULL COMMENT '规则名称',
  zone_id    VARCHAR(64) NOT NULL DEFAULT '' COMMENT '区域ID, 空串=全园区默认规则',
  rule_type  BIGINT      NOT NULL DEFAULT 1 COMMENT '1单一 2阶梯 3峰谷',
  config     JSON        NOT NULL COMMENT '规则配置(JSON 原文)',
  status     BIGINT      NOT NULL DEFAULT 1 COMMENT '1启用 0停用',
  created_at DATETIME    NOT NULL COMMENT '创建时间',
  updated_at DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_tenant_zone` (tenant_id, zone_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='电价计费规则';

-- 账单: 一行 = 某租户某区域某账期的一张账单.
CREATE TABLE IF NOT EXISTS `bill` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(0=历史数据)',
  bill_no       VARCHAR(32) NOT NULL DEFAULT '' COMMENT '账单号 B{账期}-{zone}',
  zone_id       VARCHAR(64) NOT NULL DEFAULT '' COMMENT '区域ID',
  rule_id       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '使用的规则ID',
  rule_snapshot JSON                 DEFAULT NULL COMMENT '出账时的规则快照',
  detail        JSON                 DEFAULT NULL COMMENT '计费明细(JSON)',
  period_start  DATE        NOT NULL COMMENT '账期起(含)',
  period_end    DATE        NOT NULL COMMENT '账期止(含)',
  usage_kwh     DOUBLE      NOT NULL DEFAULT 0 COMMENT '用电量(度)',
  amount        DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '应付金额(元)',
  status        BIGINT      NOT NULL DEFAULT 1 COMMENT '1待缴 2已缴 3作废',
  paid_at       DATETIME             DEFAULT NULL COMMENT '缴费时间',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_zone_period` (tenant_id, zone_id, period_start, period_end),
  KEY `idx_tenant_status` (tenant_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='电费账单';

-- 电表读数: 本服务只读, 写入方为 M4 energy-data-service.
CREATE TABLE IF NOT EXISTS `energy_reading` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '园区ID(0=历史数据/M4侧)',
  device_id   VARCHAR(64) NOT NULL COMMENT '电表设备ID',
  zone_id     VARCHAR(64) NOT NULL COMMENT '区域ID',
  energy_kwh  DOUBLE      NOT NULL COMMENT '累计读数(度)',
  reported_at DATETIME    NOT NULL COMMENT '上报时间',
  PRIMARY KEY (id),
  KEY `idx_zone_time` (zone_id, reported_at),
  KEY `idx_device_time` (device_id, reported_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='电表读数(M4 写入)';

-- ---------------------------------------------------------------------
-- 3. 公告域 (对齐 app/notice-service/internal/model)
-- ---------------------------------------------------------------------

-- 公告/通知: 1通知 2公告 3活动 4停水 5停电; 支持置顶与定时发布.
-- source: 系统自动通知的幂等键(如 workorder:assigned:{event_id}), 人工发布为 NULL.
CREATE TABLE IF NOT EXISTS `notice` (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id    BIGINT       NOT NULL COMMENT '园区ID',
  title        VARCHAR(255) NOT NULL COMMENT '标题',
  content      TEXT                  DEFAULT NULL COMMENT '公告正文',
  type         TINYINT      NOT NULL DEFAULT 1 COMMENT '1通知 2公告 3活动 4停水 5停电',
  publisher_id BIGINT       NOT NULL DEFAULT 0 COMMENT '发布人(0=系统)',
  top          TINYINT      NOT NULL DEFAULT 0 COMMENT '是否置顶 0否 1是',
  status       TINYINT      NOT NULL DEFAULT 1 COMMENT '1草稿 2已发布 3已撤回',
  publish_at   DATETIME              DEFAULT NULL COMMENT '发布时间(NULL=未发布)',
  source       VARCHAR(64)           DEFAULT NULL COMMENT '来源幂等键(系统通知), 人工发布为NULL',
  created_at   DATETIME     NOT NULL COMMENT '创建时间',
  updated_at   DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_source` (source),
  KEY `idx_tenant_status` (tenant_id, status, type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告/通知';

-- 公告已读/送达记录: 站内通知渠道按"人"落一条, read_at 为 NULL 表示已送达未读.
CREATE TABLE IF NOT EXISTS `notice_read` (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id  BIGINT   NOT NULL COMMENT '园区ID',
  notice_id  BIGINT   NOT NULL COMMENT '关联公告ID',
  user_id    BIGINT   NOT NULL COMMENT '目标用户ID',
  read_at    DATETIME          DEFAULT NULL COMMENT '已读时间(NULL=未读)',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_notice_user` (notice_id, user_id),
  KEY `idx_user_read` (tenant_id, user_id, read_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='公告已读/送达记录';

-- ---------------------------------------------------------------------
-- 4. 停车域 (对齐 app/parking-service/internal/model)
--    入场/离场由 M1 Kafka 地磁事件驱动(亦提供 HTTP 入口便于联调).
--    fee 在库中为 DECIMAL(10,2), 接口层格式化为字符串避免浮点误差.
-- ---------------------------------------------------------------------

-- 停车记录: 一条 = 某车牌某次停车(入场建记录停车中, 离场计费置已完成).
-- 统一口径(2026-09-18 评审 P0): 以带 request_id 幂等键版本为准(设计文档 §parking "按 requestId 幂等"),
--   Kafka 至少一次投递下重复入场消息靠 uk_request 唯一键去重;
--   request_id 可空: MySQL 唯一索引允许多个 NULL, 旧扁平格式/HTTP 联调直投(无 request_id)互不冲突.
CREATE TABLE IF NOT EXISTS `parking_record` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  plate_no      VARCHAR(32)  NOT NULL COMMENT '车牌号',
  request_id    CHAR(36)              DEFAULT NULL COMMENT '幂等键: M1 遥测信封 request_id, 重复投递去重',
  entry_time    DATETIME              DEFAULT NULL COMMENT '入场时间',
  exit_time     DATETIME              DEFAULT NULL COMMENT '离场时间',
  duration_min  INT                   DEFAULT NULL COMMENT '停车时长(分钟)',
  fee           DECIMAL(10,2) NOT NULL DEFAULT 0 COMMENT '停车费(元)',
  vehicle_type  TINYINT     NOT NULL DEFAULT 1 COMMENT '1月卡 2临时 3VIP 4异常',
  status        TINYINT     NOT NULL DEFAULT 1 COMMENT '1停车中 2已完成',
  device_id_in  VARCHAR(64) NOT NULL DEFAULT '' COMMENT '入场地磁设备ID',
  device_id_out VARCHAR(64) NOT NULL DEFAULT '' COMMENT '出场地磁设备ID',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_request` (request_id),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_plate_entry` (plate_no, entry_time),
  KEY `idx_status` (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车记录';

-- 停车计费规则: 以 JSON 存储阶梯/封顶等配置(当前代码为固定规则, 预留可配置).
CREATE TABLE IF NOT EXISTS `parking_fee_rule` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID',
  rule_json     JSON                 DEFAULT NULL COMMENT '计费规则(JSON)',
  effective_from DATETIME            DEFAULT NULL COMMENT '生效起',
  effective_to   DATETIME            DEFAULT NULL COMMENT '生效止',
  created_at    DATETIME    NOT NULL COMMENT '创建时间',
  updated_at    DATETIME    NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_tenant` (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车计费规则';

-- 停车月卡: 一条 = 某车牌一段有效期的月卡(P2 月卡管理).
-- 入场时按 (tenant_id, plate_no, 生效状态, 时间窗) 识别月卡车, 月卡车计费 fee=0;
-- 不建 (tenant_id, plate_no) 唯一键: 停用/过期后允许同车牌再次办卡, 识别只看 status+时间窗.
CREATE TABLE IF NOT EXISTS `monthly_card` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  plate_no      VARCHAR(32)  NOT NULL COMMENT '车牌号',
  owner_name    VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '车主姓名',
  phone         VARCHAR(20)  NOT NULL DEFAULT '' COMMENT '联系电话',
  start_time    DATETIME     NOT NULL COMMENT '生效起',
  end_time      DATETIME     NOT NULL COMMENT '生效止(到期时间)',
  status        TINYINT      NOT NULL DEFAULT 1 COMMENT '1生效 2停用',
  created_at    DATETIME     NOT NULL COMMENT '创建时间',
  updated_at    DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY `idx_tenant_plate` (tenant_id, plate_no),
  KEY `idx_tenant_end` (tenant_id, status, end_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='停车月卡';

-- ---------------------------------------------------------------------
-- 5. 访客域 (对齐 app/visitor-service/internal/model)
--    业主/物业发起邀请生成二维码, 访客扫码签入(核销+调M1开门), 签出.
--    qr_code 唯一, 签入/签出时更新 device_id 记录开门设备.
-- ---------------------------------------------------------------------

-- 访客通行记录: 一条 = 一次访客邀约/通行.
CREATE TABLE IF NOT EXISTS `visitor_record` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id     BIGINT       NOT NULL COMMENT '园区ID, RBAC 数据隔离维度',
  inviter_id    BIGINT       NOT NULL COMMENT '邀请人(业主/物业 user_id)',
  visitor_name  VARCHAR(64)  NOT NULL COMMENT '访客姓名',
  visitor_phone VARCHAR(20)  NOT NULL DEFAULT '' COMMENT '访客手机号',
  visit_time    DATETIME              DEFAULT NULL COMMENT '预期到访时间',
  expire_time   DATETIME              DEFAULT NULL COMMENT '二维码过期时间',
  qr_code       VARCHAR(512) NOT NULL COMMENT '加密二维码内容(含签名+有效期)',
  status        TINYINT      NOT NULL DEFAULT 1 COMMENT '1待使用 2已签入 3已签出 4已过期',
  checkin_at    DATETIME              DEFAULT NULL COMMENT '签入时间',
  checkout_at   DATETIME              DEFAULT NULL COMMENT '签出时间',
  device_id     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '签入/签出开门设备ID',
  blacklisted   TINYINT      NOT NULL DEFAULT 0 COMMENT '是否黑名单 0否 1是',
  created_at    DATETIME     NOT NULL COMMENT '创建时间',
  updated_at    DATETIME     NOT NULL COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY `uk_qr_code` (qr_code),
  KEY `idx_tenant` (tenant_id),
  KEY `idx_tenant_status_visit` (tenant_id, status, visit_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='访客通行记录';
