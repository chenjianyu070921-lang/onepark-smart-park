#!/usr/bin/env bash
# OnePark K8s 清单生成器(其余 9 个 CI 服务, 不含 apigateway).
# 为 .github/workflows/cd.yml matrix 中已构建的服务生成 Deployment + Service,
# 统一通过 envFrom 注入 onepark-config / onepark-secrets 中的环境变量;
# 各服务的 etc/*-api.yaml 已打进镜像, 其中 ${VAR} 在运行时由 envFrom 解析.
# 用法: cd deploy/k8s && REGISTRY=ghcr.io/your-org ./gen.sh   (输出到 ./generated/<svc>.yaml)
set -euo pipefail

REGISTRY="${REGISTRY:-ghcr.io/your-org}"
OUT_DIR="${OUT_DIR:-./generated}"
mkdir -p "$OUT_DIR"

# service:port
SERVICES=(
  "device-service:8001"
  "user-manage:8086"
  "auth-service:8088"
  "workorder-service:8082"
  "alarm-service:8009"
  "dispatch-service:8053"
  "leasing-service:8051"
  "dashboard-service:8052"
  "billing-service:8063"
)

for s in "${SERVICES[@]}"; do
  name="${s%%:*}"
  port="${s##*:}"
  cat > "$OUT_DIR/onepark-${name}.yaml" <<EOF
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
          ports:
            - containerPort: ${port}
          envFrom:
            - configMapRef:
                name: onepark-config
            - secretRef:
                name: onepark-secrets
          readinessProbe:
            tcpSocket:
              port: ${port}
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            tcpSocket:
              port: ${port}
            initialDelaySeconds: 10
            periodSeconds: 15
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
---
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
      targetPort: ${port}
EOF
  echo "generated: $OUT_DIR/onepark-${name}.yaml"
done

# 敏感凭据不再以 kind: Secret 清单入库(避免误提交真实凭据, 同时通过 CI 的 secret 扫描),
# 改为部署时用命令式方式创建(占位值, 生产请替换为真实值; 键名须与各服务 etc yaml 的 ${...} 对齐):
echo "创建 Secret(占位值, 生产请替换):"
echo "  kubectl -n onepark create secret generic onepark-secrets \\"
echo "    --from-literal=REDIS_PASS=change-me \\"
echo "    --from-literal=AUTH_SECRET=change-me"
echo "应用顺序: kubectl apply -f namespace.yaml -f configmap.yaml -f apigateway.yaml -f generated/"
