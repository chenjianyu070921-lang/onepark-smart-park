#!/bin/sh
# =====================================================================
# OnePark 数据库初始化编排脚本
# ---------------------------------------------------------------------
# 作用: 按"每服务独立库"模型, 将 deploy/sql 下各模块 DDL 应用到对应数据库.
# 触发: docker-compose mysql 容器首次初始化时, 由
#       /docker-entrypoint-initdb.d/run-all.sh 自动调用(数据卷非空后不再执行).
# 设计:
#   * SQL 文件挂载在 /sql-init(只读数据), 本脚本是唯一被镜像入口执行的 .sh.
#   * 所有 DDL 均为 CREATE DATABASE/TABLE IF NOT EXISTS 或带存在性守卫, 可重复执行.
#   * 单步失败仅告警不中止, 避免初始化异常导致容器起不来(问题由服务运行时暴露).
# =====================================================================

SQL_DIR="/sql-init"
ROOT_PWD="${MYSQL_ROOT_PASSWORD:-onepark123}"

echo "[run-all] 开始 OnePark 数据库初始化编排 (SQL_DIR=$SQL_DIR)"

# 等待 MySQL 就绪(镜像入口执行本脚本时通常已就绪, 此处兜底)
i=0
while [ "$i" -lt 60 ]; do
  if mysql --default-character-set=utf8mb4 -uroot -p"$ROOT_PWD" -e "SELECT 1" >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 2
done

# 执行单个 SQL 文件(脚本自身已含 USE / CREATE DATABASE, 无需额外指定库)
run_one() {
  f="$1"
  if [ ! -f "$SQL_DIR/$f" ]; then
    echo "[run-all] WARN: 缺少 $f, 跳过"
    return 0
  fi
  echo "[run-all] -> $f"
  # SQL 文件为 UTF-8; 显式 --default-character-set=utf8mb4 避免 mysql 客户端以 latin1 读取,
  # 否则 UTF-8 字节被当作 CP1252 二次编码, 灌入 utf8mb4 列后产生双重编码乱码(中文变 ç³»ç»Ÿ...)。
  if mysql --default-character-set=utf8mb4 -uroot -p"$ROOT_PWD" < "$SQL_DIR/$f" >/dev/null 2>&1; then
    echo "[run-all] OK:   $f"
  else
    echo "[run-all] FAIL: $f (详见 mysql 报错, 继续后续脚本)"
  fi
}

# 顺序遵循依赖: 先建库(init.sql), 再 RBAC(sys.sql), 再各业务域.
run_one init.sql                       # 20 个独立库
run_one sys.sql                       # sys_db: RBAC 5 表 + 种子管理员
run_one m1_mysql_tables.sql           # device_db
run_one m2_workorder_tables.sql       # workorder_db
run_one m2_notice_tables.sql          # notice_db
run_one m2_energy_reading.sql         # billing_db (energy_reading)
run_one billing_tables.sql            # billing_db (billing_rule/bill)
run_one m2_parking_visitor_tables.sql # parking_db / visitor_db
run_one m2_p2_monthly_card.sql        # parking_db (monthly_card, 月卡; P0 补缺, 代码已上线)
run_one m2_p2_visitor_blocklist.sql   # visitor_db (visitor_blocklist, 黑名单; 幂等 CREATE, 代码已上线)
run_one m2_p2_visitor_verify.sql      # visitor_db (visitor_record 多方式核验列, P2 落地; 幂等 ALTER, 代码已上线)
run_one m3_mysql_tables.sql           # alarm_db
run_one m3_access_mysql_tables.sql    # access_db
run_one m3_video_mysql_tables.sql     # video_db
run_one m5_mysql_tables.sql           # leasing_db / dispatch_db
run_one rbac_data_scope.sql           # sys_db: data_scope 补列(存在性守卫)

# 以下脚本为存量库迁移(ALTER 非幂等), 全新环境不需要, 跳过:
#   m5_mysql_migrations.sql / l2_tenant_id_migration.sql
# 以下为 TDengine 时序库, 非 MySQL, 跳过:
#   m1_tdengine_tables.sql

echo "[run-all] 数据库初始化编排完成"
