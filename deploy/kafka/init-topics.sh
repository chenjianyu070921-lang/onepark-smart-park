#!/usr/bin/env bash
# OnePark Kafka topic 初始化脚本.
# 幂等: 每个 topic 用 --if-not-exists 创建, 重复执行不会报错.
# 在 kafka-init 容器(复用 bitnami/kafka 镜像, 自带 kafka-topics.sh)中以一次性任务运行,
# 也可在任意装有 kafka 客户端的机器上手动执行: BOOTSTRAP=host:9092 ./init-topics.sh
set -euo pipefail

BOOTSTRAP="${KAFKA_BOOTSTRAP:-kafka:9092}"
REPLICATION="${KAFKA_REPLICATION_FACTOR:-1}"  # 单节点 KRaft 只能为 1; 多 broker 集群可改为 3

# topic:partitions
TOPICS=(
  "device-telemetry:6"     # M1 设备遥测, 吞吐高, 多分区
  "parking-entry:3"        # 车辆入场事件
  "parking-exit:3"         # 车辆离场事件
  "alarm-event:3"          # M1 原始告警(供自动建单)
  "onepark.alarm.event:3"  # M3 告警生命周期事件
  "notice-event:3"         # 公告发布事件
  "workorder-event:3"      # 工单状态事件
)

echo "[init-topics] bootstrap=${BOOTSTRAP} replication=${REPLICATION}"

for entry in "${TOPICS[@]}"; do
  topic="${entry%%:*}"
  partitions="${entry##*:}"
  # 等待 broker 可连(healthcheck 后偶发短暂未就绪, 最多重试 30 次).
  for i in $(seq 1 30); do
    if kafka-topics.sh --bootstrap-server "${BOOTSTRAP}" \
        --create --if-not-exists \
        --topic "${topic}" \
        --partitions "${partitions}" \
        --replication-factor "${REPLICATION}" 2>/tmp/kt.err; then
      echo "[init-topics] created/exists: ${topic} (partitions=${partitions})"
      break
    fi
    echo "[init-topics] retry(${i}) ${topic}: $(cat /tmp/kt.err)"
    sleep 2
  done
done

echo "[init-topics] done."
