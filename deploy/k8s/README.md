# OnePark K8s 部署清单

> **定位**：本目录是 **compose 之外的补充部署形态**，不是默认路径。项目日常开发/联调走 `deploy/docker-compose.yml`（`make up`）；K8s 用于演示多环境部署能力与后续上云预案。

## 文件清单

| 文件 | 内容 |
| --- | --- |
| `namespace.yaml` | Namespace `onepark` |
| `configmap.yaml` | ConfigMap `onepark-config`：**非机密**运行期配置（地址/分组/开关），供各服务 `envFrom` |
| `middleware.yaml` | **8 个中间件**：MySQL / Redis / TDengine / Kafka / EMQX / Nacos / MinIO / Elasticsearch（含 PVC 与 ClusterIP Service） |
| `gen.sh` | 生成器：为 **19 个业务服务**生成 Deployment + Service（探针/资源限制/envFrom/端口），并打印 Secret 与初始化 ConfigMap 的创建命令 |
| `apigateway.yaml` | 对外网关 Deployment + Service（唯一手写完整清单） |
| `init-jobs.yaml` | 3 个一次性 Job：MySQL 建表、Kafka topic 预建、Nacos 配置发布 |
| `ingress.yaml` | 对外唯一入口（只暴露网关 8080，其余全 ClusterIP） |

机密（DSN/密码/令牌）**不入库**，由命令式 `kubectl create secret` 创建 —— 见 `gen.sh` 末尾输出。

## 前置条件

1. **集群**：任一 K8s ≥ 1.24（`networking.k8s.io/v1` Ingress 与 `batch/v1` Job 要求）。
2. **StorageClass**：需有默认 SC。本目录**刻意不声明 StorageClass**（它依赖具体集群：local-path / standard / ceph…），PVC 不写 `storageClassName` 即用集群默认。
3. **Ingress Controller**：默认按 nginx 编写。用 traefik 等请改 `ingress.yaml` 的 `ingressClassName` 与注解前缀。
4. **镜像可达**：需要 `mysql:8.0`、`redis:7-alpine`、`tdengine/tdengine:3.3.3.0`、`bitnami/kafka:3.7`、`emqx/emqx:5.5.1`、`nacos/nacos-server:v2.4.3`、`coollabsio/minio:latest`、`elasticsearch:8.13.4`、`curlimages/curl:latest` 以及 19 个业务服务镜像（由根 `Dockerfile` 构建）。
5. **命令式资源**：`onepark-secrets`（Secret）+ `onepark-sql` / `onepark-kafka-scripts` / `onepark-nacos`（初始化 ConfigMap）。

## 部署顺序

```bash
cd deploy/k8s
REGISTRY=<你的镜像仓库> bash gen.sh          # 生成 ./generated/*.yaml（19 个）

# 1) 命名空间与配置
kubectl apply -f namespace.yaml -f configmap.yaml
# 2) Secret 与初始化数据（命令见 gen.sh 末尾输出）
kubectl -n onepark create secret generic onepark-secrets --from-literal=... 
kubectl -n onepark create configmap onepark-sql           --from-file=../sql
kubectl -n onepark create configmap onepark-kafka-scripts --from-file=../kafka/init-topics.sh
kubectl -n onepark create configmap onepark-nacos         --from-file=../nacos
# 3) 中间件
kubectl apply -f middleware.yaml
# 4) 应用（19 个业务服务 + 对外网关）
kubectl apply -f generated/ -f apigateway.yaml
# 5) 初始化任务（幂等，可重复 apply）
kubectl apply -f init-jobs.yaml
# 6) 对外入口
kubectl apply -f ingress.yaml
```

验证：

```bash
kubectl -n onepark get pods -w                 # 全部 Running / Job Completed
kubectl -n onepark logs job/onepark-mysql-init # 建表结果
kubectl -n onepark get ingress                 # 对外地址
curl -H 'Host: onepark.local' http://<ingress-ip>/health
```

## 与 compose 的差异（刻意为之）

| 点 | compose | K8s |
| --- | --- | --- |
| 建表时机 | MySQL 容器入口脚本（卷非空时不重跑） | **init Job**（可重复 apply，回看日志有据） |
| 服务依赖顺序 | `depends_on` + healthcheck | 无（各服务对中间件**本就设计为可降级**，缺依赖不阻断启动） |
| 上游路由 | `nacos-init` 发布上游表 | 同（`onepark-nacos-init` Job） |
| 中间件持久化 | named volume | PVC（显式声明容量） |
| 对外暴露 | 仅网关映射端口 | 仅网关 Ingress，其余 ClusterIP |

## 已验证 / 未验证（如实标注）

**已验证（离线）**

- `bash -n gen.sh` 语法通过；实跑生成 **19 个** Deployment/Service 清单，结构化断言（探针类型、端口、Service、envFrom、镜像引用）**全部通过**。
- 全部 K8s YAML 通过解析与字段断言（含本目录 8 个中间件、3 个 Job、Ingress）。

**未验证（缺环境）**

- **未在真实集群 apply 过**：本机无集群，且 Docker Hub 与所有镜像加速源均不可达（`docker manifest inspect` 全部失败），既起不了 kind/minikube，也拉不到 K8s 组件与中间件镜像。
- 因此**探针时序、PVC 绑定、Job 重试、Ingress 路由**等运行期行为均未实测。
- 容量/资源 requests-limits 为单节点演示量级，未经压测。

## 已知限制（未修复项，请勿当作已完成）

1. **dashboard-service / dispatch-service / leasing-service 三个服务的 `etc/*-api.yaml` 仍是硬编码 `127.0.0.1`**（MySQL DSN 与 Redis 6380），compose 与 K8s 注入的 DSN 都**不生效** → 容器内连不上库。这三个 yaml 自带注释「部署环境请改回 `${VAR}` 形式」，需改回。
2. **`gateway-service`（M1 设备側 TCP/CoAP 网关，非对外入口）的 `${MYSQL_DSN}` 缺对应变量定义**：compose 未部署该服务、`.env.example` 也无 `GATEWAY_MYSQL_DSN`（本目录的 Secret 提示中已补该键）。其 `gateway_db` 需自行补一个 DSN 变量。
3. **`access-control-service` / `visitor-service` 使用 `${VAR:-default}` 语法**：go-zero 的 `conf.UseEnv()` 不支持默认值语法，会取到空值 → 设备 gRPC 端点为空并走降级（不崩，但远程开门不可用）。
4. **中间件均为单副本**：MySQL/Redis/Nacos/MinIO 无主从、Kafka/EMQX/TDengine/ES 单节点，无高可用；生产需改 StatefulSet 集群形态 + 反亲和 + PDB。
5. **ES 关闭了安全（`xpack.security.enabled=false`）**：与 compose 口径一致，仅限集群内 ClusterIP 访问；生产必须开启并配证书。
6. **MySQL 在部分 WSL2/Docker Desktop 环境需放宽 seccomp**（`Can't create thread to handle bootstrap`）—— 与 compose 路径同一根因，K8s 侧同样会命中（取决于节点 runtime 配置）。
