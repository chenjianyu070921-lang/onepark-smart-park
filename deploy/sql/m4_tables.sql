-- OnePark 智慧园区 - M4 能源管控服务 建表
-- 覆盖三张表: energy_reading(能耗读数) / billing_rule(计费规则) / bill(账单)
--
-- 执行方式(任选一种):
--   1) Navicat 选中组内公用库 onepark-smart-park, 直接全选执行
--   2) 命令行: mysql -h 115.191.16.159 -u root -p onepark-smart-park < deploy/sql/m4_tables.sql
--   3) 新建环境: 把下面的 USE 换成 init.sql 里规划的独立库(energy_data_db / billing_db)
--
-- 注意(跨库的事说清楚):
--   init.sql 的规范是"每服务独立库, 禁止跨库 JOIN", 但 energy-analysis-service(8062)
--   要直接读 energy_reading 做日报/月报聚合, 而这张表归 energy-data-service 管。
--   组内目前的做法是三个服务都连同一个公用库 onepark-smart-park, 所以不存在跨库问题;
--   将来真要严格分库, 要么给 analysis 服务开只读账号跨库查, 要么由 data 服务出聚合接口。

-- ============================================================
-- energy_reading  能耗读数(属 energy-data-service 写, analysis/billing 只读)
-- 每台电表每次上报一行, 存的是"累计读数"不是"用了多少"
-- ============================================================
CREATE TABLE IF NOT EXISTS `energy_reading` (
  `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `device_id`   VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '设备编号, 对应 M1 device.device_id',
  `zone_id`     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '区域, 如 A栋/B栋/车库, 查不到归属时填未分配',
  `energy_kwh`  DOUBLE       NOT NULL DEFAULT 0 COMMENT '电表累计读数(度), 只增不减',
  `power_kw`    DOUBLE       NULL DEFAULT NULL COMMENT '瞬时功率(kW), 展示用, 可空',
  `reported_at` DATETIME     NOT NULL COMMENT '设备采集时间(不是入库时间)',
  `created_at`  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '入库时间',
  PRIMARY KEY (`id`),
  KEY `idx_device_time` (`device_id`, `reported_at`),
  KEY `idx_zone_time` (`zone_id`, `reported_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='能耗读数, 每次上报一行';

-- ============================================================
-- billing_rule  计费规则(价目表)
-- ============================================================
CREATE TABLE IF NOT EXISTS `billing_rule` (
  `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `name`       VARCHAR(64) NOT NULL DEFAULT '' COMMENT '规则名称',
  `zone_id`    VARCHAR(64) NOT NULL DEFAULT '' COMMENT '适用区域, 空串=全园区默认规则',
  `rule_type`  TINYINT     NOT NULL DEFAULT 1 COMMENT '1单一电价 2阶梯电价 3峰谷电价',
  `config`     JSON        NOT NULL COMMENT '规则明细, 见下面三种格式',
  `status`     TINYINT     NOT NULL DEFAULT 1 COMMENT '1启用 0停用',
  `created_at` DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_zone_status` (`zone_id`, `status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='计费规则(价目表)';

-- config 字段三种写法(rule_type 决定用哪种):
--   1 单一: {"price":0.65}
--   2 阶梯: {"tiers":[{"upTo":100,"price":0.6},{"upTo":300,"price":0.8},{"upTo":null,"price":1}]}
--           upTo 是本档上限(度), 必须递增; 最后一档 upTo 传 null 表示不封顶
--   3 峰谷: {"base":0.6,"periods":[{"name":"峰","from":"08:00","to":"11:00","price":1.05},
--                                  {"name":"谷","from":"23:00","to":"07:00","price":0.35}]}
--           base 是平段电价; from>to 表示跨零点(如 23:00→07:00); 不在任何时段内按 base 算

-- ============================================================
-- bill  能耗账单
-- ============================================================
CREATE TABLE IF NOT EXISTS `bill` (
  `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `bill_no`       VARCHAR(32) NOT NULL DEFAULT '' COMMENT '账单号, 如 B202609-A01',
  `zone_id`       VARCHAR(64) NOT NULL DEFAULT '' COMMENT '计费区域',
  `rule_id`       BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '出账时用的规则',
  `rule_snapshot` JSON DEFAULT NULL COMMENT '出账那一刻的规则快照, 防止以后改规则导致历史账单对不上',
  `detail`        JSON DEFAULT NULL COMMENT '计费明细: 阶梯每一档/峰谷每一段的用量与金额, 解释"这钱怎么算出来的"',
  `period_start`  DATE NOT NULL COMMENT '账期开始(含)',
  `period_end`    DATE NOT NULL COMMENT '账期结束(含)',
  `usage_kwh`     DOUBLE NOT NULL DEFAULT 0 COMMENT '区间用量(度)',
  `amount`        DECIMAL(12,2) NOT NULL DEFAULT 0.00 COMMENT '金额(元)',
  `status`        TINYINT NOT NULL DEFAULT 1 COMMENT '1待缴 2已缴 3作废',
  `paid_at`       DATETIME DEFAULT NULL COMMENT '缴纳时间',
  `created_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_bill_no` (`bill_no`),
  KEY `idx_zone_period` (`zone_id`, `period_start`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='能耗账单';

-- bill_no 唯一, 所以同一区域同一账期只能出一次账(重复出账会被 uk_bill_no 挡住)
