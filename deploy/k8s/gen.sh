#!/usr/bin/env bash
# OnePark K8s 清单生成器(全部 19 个业务服务; 对外入口 apigateway 见同目录 apigateway.yaml).
# 为 CD 流水线中已构建的服务生成 Deployment + Service,
# 统一通过 envFrom 注入 onepark-config / onepark-secrets 中的环境变量;
# 各服务的 etc/*-api.yaml 已打进镜像, 其中 ${VAR} 在运行时由 envFrom 解析.
# 探针口径与 deploy/docker-compose.yml 保持一致: REST 服务探 HTTP /health(common/health 注册),
# 无 HTTP 端点的 TCP/gRPC 服务退化为 tcpSocket.
# 用法: cd deploy/k8s && REGISTRY=ghcr.io/your-org ./gen.sh   (输出到 ./generated/<svc>.yaml)
set -euo pipefail

REGISTRY="${REGISTRY:-ghcr.io/your-org}"
OUT_DIR="${OUT_DIR:-./generated}"
mkdir -p "$OUT_DIR"

# 服务清单: name|端口|探针类型|额外暴露端口(逗号分隔, 可空)
#   http: REST 服务, 探 GET /health
#   tcp : 无 HTTP 端点的 TCP/gRPC 服务(gateway-service 设备接入 / shadow 影子 gRPC)
#   none: 纯后台进程(无监听端口), 不配探针与 Service
#   额外端口取自各服务 etc 下的 gRPC 配置: device 9001 / alarm 9009 / workorder 9091 /
#   energy-data 9061 / leasing 9051; shadow 的 9002 即其主端口.
SERVICES=(
  "device-service:8001:http:9001"
  "gateway-service:7000:tcp:"
  "shadow-service:9002:tcp:"
  "event-dispatcher:0:none:"
  "workorder-service:8082:http:9091"
  "visitor-service:8083:http:"
  "parking-service:8084:http:"
  "notice-service:8085:http:"
  "access-control-service:8010:http:"
  "video-service:8011:http:"
  "alarm-service:8009:http:9009"
  "energy-data-service:8061:http:9061"
  "energy-analysis-service:8062:http:"
  "billing-service:8063:http:"
  "leasing-service:8051:http:9051"
  "dashboard-service:8052:http:"
  "dispatch-service:8053:http:"
  "auth-service:8088:http:"
  "user-manage:8086:http:"
)

for item in "${SERVICES[@]}"; do
  IFS=':' read -r name port probe extra_ports <<<"$item"

  # 容器端口块(含 gRPC 端口时一并暴露, 便于集群内服务间 RPC).
  ports_block=""
  probe_block=""
  service_block=""

  # 别名注入: 这 3 个服务的 etc/*.yaml 引用的是统一名 ${MYSQL_DSN},
  # 而 envFrom 只能原样注入键名、不能改名 —— compose 侧用 `MYSQL_DSN: ${DEVICE_MYSQL_DSN}`
  # 做了映射(deploy/docker-compose.yml:232/821), 此处等价补上, 否则容器内 DSN 恒为空。
  alias_env=""
  case "$name" in
    device-service) alias_key="DEVICE_MYSQL_DSN" ;;
    shadow-service) alias_key="SHADOW_MYSQL_DSN" ;;
    gateway-service) alias_key="GATEWAY_MYSQL_DSN" ;;
    *) alias_key="" ;;
  esac
  if [ -n "$alias_key" ]; then
    alias_env="          env:
            - name: MYSQL_DSN
              valueFrom:
                secretKeyRef:
                  name: onepark-secrets
                  key: ${alias_key}"
  fi
  if [ "$probe" != "none" ]; then
    ports_block="          ports:
            - containerPort: ${port}"
    IFS=',' read -ra grpc_ports <<<"$extra_ports"
    for gp in "${grpc_ports[@]}"; do
      [ -z "$gp" ] && continue
      ports_block="${ports_block}
            - containerPort: ${gp}"
    done

    if [ "$probe" = "http" ]; then
      probe_block="          readinessProbe:
            httpGet:
              path: /health
              port: ${port}
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /health
              port: ${port}
            initialDelaySeconds: 10
            periodSeconds: 15"
    else
      probe_block="          readinessProbe:
            tcpSocket:
              port: ${port}
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            tcpSocket:
              port: ${port}
            initialDelaySeconds: 10
            periodSeconds: 15"
    fi

    service_block="---
apiVersion: v1
kind: Service
metadata:
  name: onepark-${name}
  namespace: onepark
spec:
  type: ClusterIP
  selector:
    app: onepark-${name}
  ports:
    - port: ${port}
      targetPort: ${port}"
  fi

  {
    cat <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: onepark-${name}
  namespace: onepark
  labels:
    app: onepark-${name}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: onepark-${name}
  template:
    metadata:
      labels:
        app: onepark-${name}
    spec:
      containers:
        - name: ${name}
          image: ${REGISTRY}/onepark/${name}:latest
${ports_block}
          envFrom:
            - configMapRef:
                name: onepark-config
            - secretRef:
                name: onepark-secrets
${alias_env}
${probe_block}
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
EOF
    [ -n "$service_block" ] && printf '%s\n' "$service_block"
  } > "$OUT_DIR/onepark-${name}.yaml"

  echo "generated: $OUT_DIR/onepark-${name}.yaml"
done

# 敏感凭据与初始化数据一律不入库(避免误提交真实凭据, 同时通过 CI 的 secret 扫描),
# 改为部署时命令式创建; 键名必须与各服务 etc/*.yaml 的 ${...} 占位符严格一致。
echo
echo "1) 创建 Secret(占位值, 生产务必替换; 取值口径见 deploy/.env.example):"
cat <<'HINT'
  kubectl -n onepark create secret generic onepark-secrets \
    --from-literal=MYSQL_ROOT_PASSWORD=change-me \
    --from-literal=REDIS_PASSWORD=change-me \
    --from-literal=REDIS_PASS=change-me \
    --from-literal=AUTH_REDIS_PASS=change-me \
    --from-literal=TDENGINE_PASSWORD=change-me \
    --from-literal=TDENGINE_DSN='root:change-me@tcp(tdengine:6030)/device' \
    --from-literal=MINIO_ROOT_USER=change-me \
    --from-literal=MINIO_ROOT_PASSWORD=change-me \
    --from-literal=WORKORDER_MINIO_ACCESS_KEY=change-me \
    --from-literal=WORKORDER_MINIO_SECRET_KEY=change-me \
    --from-literal=ES_PASSWORD=change-me \
    --from-literal=NACOS_AUTH_TOKEN=change-me \
    --from-literal=NACOS_AUTH_KEY=change-me \
    --from-literal=NACOS_AUTH_VALUE=change-me \
    --from-literal=NACOS_USERNAME=nacos \
    --from-literal=NACOS_PASSWORD=nacos \
    --from-literal=JWT_SECRET=change-me \
    --from-literal=AUTH_SECRET=change-me \
    --from-literal=VIDEO_STREAM_SIGN_SECRET=change-me \
    --from-literal=DEVICE_MYSQL_DSN='root:change-me@tcp(mysql:3306)/device_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=SHADOW_MYSQL_DSN='root:change-me@tcp(mysql:3306)/shadow_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=GATEWAY_MYSQL_DSN='root:change-me@tcp(mysql:3306)/gateway_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=AUTH_MYSQL_DSN='root:change-me@tcp(mysql:3306)/sys_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=WORKORDER_MYSQL_DSN='root:change-me@tcp(mysql:3306)/workorder_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=VISITOR_MYSQL_DSN='root:change-me@tcp(mysql:3306)/visitor_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=PARKING_MYSQL_DSN='root:change-me@tcp(mysql:3306)/parking_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=NOTICE_MYSQL_DSN='root:change-me@tcp(mysql:3306)/notice_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=ALARM_MYSQL_DSN='root:change-me@tcp(mysql:3306)/alarm_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=ACCESS_MYSQL_DSN='root:change-me@tcp(mysql:3306)/access_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=VIDEO_MYSQL_DSN='root:change-me@tcp(mysql:3306)/video_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=ENERGY_DATA_MYSQL_DSN='root:change-me@tcp(mysql:3306)/billing_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=ENERGY_ANALYSIS_MYSQL_DSN='root:change-me@tcp(mysql:3306)/energy_analysis_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=LEASING_MYSQL_DSN='root:change-me@tcp(mysql:3306)/leasing_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=DASHBOARD_MYSQL_DSN='root:change-me@tcp(mysql:3306)/dashboard_db?charset=utf8mb4&parseTime=True&loc=Local' \
    --from-literal=DISPATCH_MYSQL_DSN='root:change-me@tcp(mysql:3306)/dispatch_db?charset=utf8mb4&parseTime=True&loc=Local'
HINT
echo
echo "2) 创建初始化数据 ConfigMap(供 init-jobs.yaml 挂载):"
echo "  kubectl -n onepark create configmap onepark-sql           --from-file=deploy/sql"
echo "  kubectl -n onepark create configmap onepark-kafka-scripts --from-file=deploy/kafka/init-topics.sh"
echo "  kubectl -n onepark create configmap onepark-nacos         --from-file=deploy/nacos"
echo
echo "3) 应用(顺序: 命名空间 -> 配置 -> 中间件 -> 应用 -> 初始化 Job -> 对外入口):"
echo "  kubectl apply -f namespace.yaml -f configmap.yaml"
echo "  kubectl apply -f middleware.yaml"
echo "  kubectl apply -f generated/ -f apigateway.yaml"
echo "  kubectl apply -f init-jobs.yaml"
echo "  kubectl apply -f ingress.yaml"
