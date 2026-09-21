# 本地 Kafka（M5 端到端验证用）

本目录服务**本地联调**：让 M5 的 Kafka 端到端验证不依赖组长那个共享 broker。
生产/联调环境的 broker 由平台侧提供，见 `deploy/docker-compose.yml` 的 `kafka` + `kafka-init`。

> **为什么要有本地 broker**：共享 broker 上的 `alarm-event` 是否已建、能不能写，权责在组长；
> 而「M5 消费者能不能正确消费」这件事**根本不需要**共享 broker 就能验证完。

---

## 一、起 broker

### 方式 A：用镜像源拉官方镜像（推荐）

⭐ **必须配双监听器**：宿主机服务与容器内 CLI 需要不同的地址，
单监听器必然有一方连不上（下面坑 1 有完整的失败现象与原理）。

```powershell
docker pull docker.m.daocloud.io/apache/kafka:3.7.0

docker run -d --name onepark-kafka-local -p 19092:19092 `
  -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller `
  -e KAFKA_LISTENERS=INTERNAL://:9092,EXTERNAL://:19092,CONTROLLER://:9093 `
  -e KAFKA_ADVERTISED_LISTENERS=INTERNAL://localhost:9092,EXTERNAL://127.0.0.1:19092 `
  -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=INTERNAL:PLAINTEXT,EXTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT `
  -e KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL `
  -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER `
  -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093 `
  -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 `
  docker.m.daocloud.io/apache/kafka:3.7.0
```

两条通路各管一边（**已实跑验证**）：

| 客户端 | 连的地址 | broker 回给它的 advertised 地址 |
|---|---|---|
| **宿主机**上的服务 / 工具（`go run`） | `127.0.0.1:19092` | `EXTERNAL://127.0.0.1:19092` ✅ |
| **本容器内**的 CLI（`docker exec`） | `localhost:9092` | `INTERNAL://localhost:9092` ✅ |

### ⭐ 三个必踩的坑

**1. `ADVERTISED_LISTENERS` 必须是「客户端能连上的那个地址」，不是容器内地址。**

Kafka 客户端第一次连上 broker 后，broker 会**把 advertised 地址回给客户端**，客户端再拿它去连。
所以：只配一个监听器时，**宿主机和容器内 CLI 必有一方连不上** —— 实测现象：

```
# 单监听器 ADVERTISED_LISTENERS=PLAINTEXT://127.0.0.1:19092 时, 在容器内执行建 topic:
WARN Connection to node 1 (/127.0.0.1:19092) could not be established
ERROR Timed out waiting for a node assignment. Call: listTopics
```

容器内的 `127.0.0.1` 是**容器自己**，不是 broker，而 broker 偏偏把它指给了客户端。
**双监听器就是为解这件事而存在的**（上面的方式 A）。

同理，`deploy/docker-compose.yml` 里写 `PLAINTEXT://kafka:9092` 是**给同网络内容器用的**，
本机直跑的服务**不能**直接用它。

**2. 容器内的 CLI 要用 `localhost:9092`，不能用容器名。**

INTERNAL 广告地址写成容器名（如 `onepark-kafka-local:9092`）时，容器内解析不了自己的名字：

```
ConfigException: No resolvable bootstrap urls given in bootstrap.servers
```

写成 `localhost:9092` 即可（同容器内客户端用）。

**3. 端口用 19092，不要用 9092。**
配置里残留着共享 broker 的地址，同端口容易让人分不清「连的到底是哪个 broker」。
（`deploy/.env` 里 `KAFKA_PORT=9092` 是给 compose 那套用的，别混。）

### 方式 B：镜像拉不下来时（换更轻的 redpanda，Kafka API 兼容）

```powershell
docker pull docker.m.daocloud.io/redpanda/redpanda:v24.2.7

docker run -d --name onepark-kafka-local -p 19092:9092 `
  docker.m.daocloud.io/redpanda/redpanda:v24.2.7 `
  redpanda start --overprovisioned --smp 1 --memory 1G --reserve-memory 0M `
  --node-id 0 --check=false `
  --kafka-addr PLAINTEXT://0.0.0.0:9092 `
  --advertise-kafka-addr PLAINTEXT://127.0.0.1:19092
```

redpanda 单容器启动、无 JVM，起得比 Kafka 快得多；`--advertise-kafka-addr` 同理必须是 `127.0.0.1:19092`。

---

## 二、建 topic

本地随便建，不涉及任何授权。

```powershell
# 用仓库里统一的建 topic 脚本（幂等，可重复执行）
docker cp deploy/kafka/init-topics.sh onepark-kafka-local:/scripts/init-topics.sh
docker exec onepark-kafka-local bash -c `
  'KAFKA_BOOTSTRAP=localhost:9092 /scripts/init-topics.sh'
```

**M5 端到端只需要其中一个**（`alarm-event`，就是 dispatch 消费者的 topic）：

```powershell
# ⚠️ 两处都不能省: ① 脚本要全路径(apache/kafka 镜像不在 PATH 里)
#              ② bootstrap 要用 INTERNAL 的 localhost:9092(容器内), 不是宿主机的 19092
docker exec onepark-kafka-local /opt/kafka/bin/kafka-topics.sh `
  --bootstrap-server localhost:9092 `
  --create --if-not-exists --topic alarm-event --partitions 3 --replication-factor 1
```

建完核对：

```powershell
docker exec onepark-kafka-local /opt/kafka/bin/kafka-topics.sh `
  --bootstrap-server localhost:9092 --list
```

> ⚠️ **该镜像关闭了自动建 topic**（实测：向不存在的 topic 投递会报 `Unknown Topic Or Partition`），
> 所以**必须先建 topic 再投递** —— 这也是好事：它和共享 broker 的严格口径一致，不会偷偷建出偏序。

---

## 三、清场

```powershell
# 停并删 broker（含数据卷）—— 下次要干净环境就删卷，否则历史消息还在
docker rm -f onepark-kafka-local

# 只想重置消费位移（不删 broker）: 换消费组名是最省事的办法
#   消费组名由 M5-Kafka-Group 决定(临时配置里改), 旧组删掉即可:
docker exec onepark-kafka-local kafka-consumer-groups.sh `
  --bootstrap-server 127.0.0.1:9092 --delete --group m5-dispatch-e2e-local
```

⚠️ **`common/kafka.NewConsumer` 固定 `StartOffset=FirstOffset`** —— 新消费组会**从最早位移开始消费**。
本地反复验证时若不想被历史消息干扰，就换个消费组名，或在投递前先清场。

---

## 四、排障速查

| 现象 | 原因 |
|---|---|
| `dial tcp: lookup kafka ... no such host` | `ADVERTISED_LISTENERS` 写成了容器内地址（见坑 1） |
| 投递成功但消费端收不到 | 消费组已有位移且 topic 里是旧消息；换消费组名或清位移 |
| 服务起来就报 `connection refused` | broker 没起 / 端口不是 19092 / `Kafka.Enabled` 没打开 |
| `consumer` 日志只打印“开始消费”但无建单 | 消息体缺 `request_id` 或 `device_id` → 被当非法消息丢弃（这是**正确**行为，见 `consumer/alarm.go`） |
