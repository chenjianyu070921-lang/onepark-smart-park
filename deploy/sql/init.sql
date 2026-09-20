-- OnePark 智慧园区 - 数据库初始化
-- 容器启动时自动执行, 预创建 20 个业务库 (每服务独立库, 禁止跨库 JOIN)

CREATE DATABASE IF NOT EXISTS device_db        DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS gateway_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS shadow_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS event_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS workorder_db    DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS visitor_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS parking_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS notice_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS alarm_db        DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS access_db       DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS video_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS energy_data_db  DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS energy_analysis_db DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS billing_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS leasing_db      DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS dashboard_db    DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS dispatch_db     DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

CREATE DATABASE IF NOT EXISTS auth_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS user_db         DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS gateway_route_db DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- RBAC 公共表 (M6 user_db 内, 五表模型)
USE user_db;
CREATE TABLE IF NOT EXISTS `user` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username    VARCHAR(64)  NOT NULL DEFAULT '',
  password    VARCHAR(255) NOT NULL DEFAULT '',
  nickname    VARCHAR(64)  NOT NULL DEFAULT '',
  phone       VARCHAR(20)  NOT NULL DEFAULT '',
  status      TINYINT      NOT NULL DEFAULT 1 COMMENT '1启用 0禁用',
  created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `role` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)  NOT NULL DEFAULT '',
  code        VARCHAR(64)  NOT NULL DEFAULT '',
  created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `permission` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)  NOT NULL DEFAULT '',
  code        VARCHAR(128) NOT NULL DEFAULT '',
  type        TINYINT      NOT NULL DEFAULT 1 COMMENT '1菜单 2按钮 3接口',
  PRIMARY KEY (id),
  UNIQUE KEY uk_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `user_role` (
  user_id     BIGINT UNSIGNED NOT NULL,
  role_id     BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (user_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `role_permission` (
  role_id        BIGINT UNSIGNED NOT NULL,
  permission_id  BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- ================= M4 能源管控 (组内公用库) =================
-- 说明: 组内实际做法是共用一个大库(与上方"每服务独立库"约定不同), M4 三张表统一建在公用库
-- 注意: 库名带横线, SQL 里必须用反引号 ` 包起来
-- Navicat 手动执行: 把下面 CREATE DATABASE 到文件末尾的部分整体粘贴执行即可
CREATE DATABASE IF NOT EXISTS `onepark-smart-park` DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `onepark-smart-park`;

-- 1. 能耗读数表: 设备每上报一次就加一行, 接口 52/53/54/56/57/58 全从这张表查
--    用量算法: 同一设备一段时间内 最后一条读数 - 最前一条读数 = 这段时间用了多少度
CREATE TABLE IF NOT EXISTS `energy_reading` (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  device_id    VARCHAR(64) NOT NULL DEFAULT '' COMMENT '设备编号, 对应 device 表的编号',
  zone_id      VARCHAR(64) NOT NULL DEFAULT '' COMMENT '区域, 如 A栋/B栋/车库, 上报时由消息带上',
  energy_kwh   DOUBLE      NOT NULL DEFAULT 0  COMMENT '电表累计读数(度), 只增不减',
  power_kw     DOUBLE      NULL                COMMENT '瞬时功率(kW), 实时页面展示用, 可空',
  reported_at  DATETIME    NOT NULL            COMMENT '设备采集时间(不是入库时间)',
  created_at   DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '入库时间',
  PRIMARY KEY (id),
  KEY idx_device_time (device_id, reported_at),
  KEY idx_zone_time (zone_id, reported_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='能耗读数, 每次上报一行';

-- 2. 计费规则表: 一度电多少钱, 三种规则类型的 config 长这样:
--    单一(rule_type=1): {"price":0.65}
--    阶梯(rule_type=2): {"tiers":[{"upTo":100,"price":0.60},{"upTo":300,"price":0.80},{"upTo":null,"price":1.00}]}
--    峰谷(rule_type=3): {"base":0.60,"periods":[{"name":"峰","from":"08:00","to":"11:00","price":1.05},{"name":"谷","from":"23:00","to":"07:00","price":0.35}]}
CREATE TABLE IF NOT EXISTS `billing_rule` (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则名称, 如 "A栋商业电价"',
  zone_id     VARCHAR(64) NOT NULL DEFAULT '' COMMENT '适用区域, 空串=全园区默认规则',
  rule_type   TINYINT     NOT NULL DEFAULT 1  COMMENT '1单一 2阶梯 3峰谷',
  config      JSON        NOT NULL COMMENT '规则明细, 结构见上方注释',
  status      TINYINT     NOT NULL DEFAULT 1  COMMENT '1启用 0停用',
  created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_zone_status (zone_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='计费规则(价目表)';

-- 3. 账单表: 每月定时任务算一次, 一行 = 某区域某个月用了多少度、该交多少钱
CREATE TABLE IF NOT EXISTS `bill` (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  bill_no       VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '账单号, 如 B202609-A01',
  zone_id       VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '计费区域',
  rule_id       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '出账时用的规则',
  rule_snapshot JSON             COMMENT '出账那一刻的规则快照, 防止以后改规则导致历史账单对不上',
  detail        JSON             COMMENT '计费明细: 阶梯每一档/峰谷每一段的用量与金额, 用于解释"这钱怎么算出来的"',
  period_start  DATE         NOT NULL COMMENT '账期开始(含)',
  period_end    DATE         NOT NULL COMMENT '账期结束(含)',
  usage_kwh     DOUBLE       NOT NULL DEFAULT 0 COMMENT '区间用量(度)',
  amount        DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '金额(元)',
  status        TINYINT      NOT NULL DEFAULT 1 COMMENT '1待缴 2已缴 3作废',
  paid_at       DATETIME     NULL,
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_bill_no (bill_no),
  KEY idx_zone_period (zone_id, period_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='能耗账单';
