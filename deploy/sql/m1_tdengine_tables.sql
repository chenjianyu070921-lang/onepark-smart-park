-- ============================================================
-- OnePark M1 物联接入底座 - TDengine 超表
-- 数据库：onepark_ts
-- 执行方式：taos -f m1_tdengine_tables.sql 或在 taos CLI 中逐行执行
-- 设计原则：一设备一子表，同类设备一张超表；Tag 装静态属性，Column 装时序数据
-- ============================================================

-- ############################################################
-- 0. 创建时序库
-- ############################################################
CREATE DATABASE IF NOT EXISTS onepark_ts
  KEEP 365          -- 数据保留 365 天
  DURATION 10       -- 每 10 天一个数据文件
  PRECISION 'ms';   -- 毫秒级精度

USE onepark_ts;

-- ############################################################
-- 1. meter_elec 电表超表
-- ############################################################
CREATE STABLE IF NOT EXISTS meter_elec (
  ts              TIMESTAMP,        -- 采集时间戳
  voltage_a       DOUBLE,           -- A 相电压 (V)
  voltage_b       DOUBLE,           -- B 相电压 (V)
  voltage_c       DOUBLE,           -- C 相电压 (V)
  current_a       DOUBLE,           -- A 相电流 (A)
  current_b       DOUBLE,           -- B 相电流 (A)
  current_c       DOUBLE,           -- C 相电流 (A)
  active_power    DOUBLE,           -- 有功功率 (W)
  reactive_power  DOUBLE,           -- 无功功率 (var)
  power_factor    DOUBLE,           -- 功率因数
  energy_total    DOUBLE,           -- 累计电量 (kWh, 计费依据)
  status          TINYINT           -- 0离线 1在线 2故障
) TAGS (
  device_id     BINARY(32),         -- 设备唯一 ID (外键 device_db.device.device_id)
  park_id       BINARY(16),         -- 园区 ID
  building_id   BINARY(16),         -- 楼栋 ID
  floor         BINARY(8),          -- 楼层
  device_model  BINARY(32)          -- 设备型号
);

-- ############################################################
-- 2. meter_water 水表超表
-- ############################################################
CREATE STABLE IF NOT EXISTS meter_water (
  ts          TIMESTAMP,            -- 采集时间戳
  flow_rate   DOUBLE,               -- 瞬时流量 (L/h)
  total_flow  DOUBLE,               -- 累计流量 (m³, 计费依据)
  pressure    DOUBLE,               -- 管道压力 (MPa)
  status      TINYINT               -- 0离线 1在线 2故障
) TAGS (
  device_id     BINARY(32),
  park_id       BINARY(16),
  building_id   BINARY(16),
  floor         BINARY(8),
  device_model  BINARY(32)
);

-- ############################################################
-- 3. device_telemetry 通用遥测超表 (EAV 模型)
-- 适用于：温湿度、PM2.5、CO2、车位地磁等异构低频设备
-- ############################################################
CREATE STABLE IF NOT EXISTS device_telemetry (
  ts          TIMESTAMP,            -- 采集时间戳
  metric_name BINARY(32),           -- 指标名: temperature/humidity/pm25/co2/parking...
  value       DOUBLE,               -- 指标值
  quality     TINYINT               -- 数据质量 0好 1差 2缺失
) TAGS (
  device_id    BINARY(32),
  park_id      BINARY(16),
  building_id  BINARY(16),
  device_type  BINARY(16)           -- sensor/access/parking/...
);

-- ############################################################
-- 4. device_event 设备事件超表
-- ############################################################
CREATE STABLE IF NOT EXISTS device_event (
  ts          TIMESTAMP,            -- 事件时间戳
  event_type  BINARY(16),           -- online/offline/fault/reboot...
  severity    TINYINT,              -- 1 info 2 warn 3 error
  message     BINARY(128)           -- 事件描述
) TAGS (
  device_id    BINARY(32),
  park_id      BINARY(16),
  device_type  BINARY(16)
);

-- ############################################################
-- 5. 降采样流（可选，开启后自动把秒级聚合成分钟级）
-- ############################################################
-- 电表分钟级降采样（如需开启取消注释）
-- CREATE STREAM IF NOT EXISTS s_meter_elec_1m
--   INTO meter_elec_1m
--   AS
--   SELECT _wstart AS ts,
--          AVG(voltage_a)   AS voltage_a,
--          AVG(active_power) AS active_power,
--          LAST(energy_total) AS energy_total
--   FROM meter_elec
--   INTERVAL(1m);

-- 水表分钟级降采样
-- CREATE STREAM IF NOT EXISTS s_meter_water_1m
--   INTO meter_water_1m
--   AS
--   SELECT _wstart AS ts,
--          AVG(flow_rate) AS flow_rate,
--          LAST(total_flow) AS total_flow
--   FROM meter_water
--   INTERVAL(1m);

-- ============================================================
-- 验证
-- ============================================================
SHOW STABLES;
