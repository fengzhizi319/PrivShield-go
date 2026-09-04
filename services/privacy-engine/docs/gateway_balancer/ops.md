# 代理转发与负载均衡网关运维与部署手册 (Operations Guide)

> 本手册为 `PrivShield` 代理转发与负载均衡网关（API Gateway & Load Balancer）的生产级部署、运维管理、安全加固、与 Kubernetes 负载均衡协同、可观测性与故障排查提供端到端的操作指南与标准作业程序（SOP）。
>
> 关联设计文档：[代理转发与负载均衡网关设计与实现规范](docs/gateway_balancer/design.md)

---

## 1. 部署拓扑与架构模式

`PrivShield` 代理网关主要包含以下两种典型生产部署架构：

### 1.1 独立集群调度模式 (Standalone Clustered Gateway)

网关作为独立的高可用入口层部署，承接所有外部客户端的 REST 与 gRPC 流量，并通过自适应调度算法（P2C-EWMA、平滑加权轮询 SWRR、最小连接数等）分发到后端的多个 `PrivShield Agent` 计算节点。

```mermaid
graph TD
    Client[客户端集群] -->|"REST (8000) / gRPC (50000)"| GW[PrivShield Gateway 集群]
    
    subgraph GatewayLayer ["网关调度层 (bin/privshield-gateway)"]
        GW
    end

    subgraph WorkerCluster [Agent 计算节点集群]
        W1[Agent Worker 1<br/>REST:8079 / gRPC:50051]
        W2[Agent Worker 2<br/>REST:8080 / gRPC:50052]
        W3[Agent Worker 3<br/>REST:8081 / gRPC:50053]
    end

    subgraph Storage [持久化存储]
        BudgetDB[("共享原子预算库<br/>privacy-go-sdk/budget")]
    end

    GW -->|P2C-EWMA / SWRR / 最小连接数| W1
    GW -->|双协议透明流代理| W2
    GW -->|三态熔断自愈| W3

    W1 -.->|原子 CAS 循环| BudgetDB
    W2 -.->|无锁高并发记账| BudgetDB
    W3 -.->|租户命名空间隔离| BudgetDB
```

### 1.2 云原生 Kubernetes 双层协同模式 (K8s Ingress + Gateway + Headless Agent)

在云原生 Kubernetes 部署中，**K8s 平台级网络能力（L4/L7 Ingress）** 与 **PrivShield Gateway 应用级精细调度能力（L7 per-RPC 动态分发）** 双层协同：

- **外层（南北向接入）**：由 Kubernetes Ingress Controller（如 Nginx Ingress / Envoy Gateway）或云厂商负载均衡器（ALB/CLB）暴露公网 IP，负责大带宽汇聚、集群外部域名接入与 SSL 卸载，将流量均匀分发至多个 `PrivShield Gateway` Pod。
- **内层（东西向调度）**：由 `PrivShield Gateway` 负责应用层 L7 治理，利用 **Headless Service（`clusterIP: None`）** 直接感知后端每个 `PrivShield Agent` Pod，实施 **per-RPC 级 gRPC 负载均衡**、**P2C-EWMA 调度**、**毫秒级故障隔离**与**三态熔断保护**。

```mermaid
graph TD
    Client[外部客户端集群] -->|公网 HTTPS / gRPC| Ingress[K8s Ingress Controller / Cloud LB]

    subgraph K8sCluster [Kubernetes 集群内部]
        Ingress -->|L4/L7 轮询分发| GW1[PrivShield Gateway Pod 1]
        Ingress -->|L4/L7 轮询分发| GW2[PrivShield Gateway Pod 2]

        subgraph HeadlessAgentSvc ["Agent Headless Service (clusterIP: None)"]
            GW1 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod1["Agent Pod 1 (10.244.1.10)"]
            GW1 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod2["Agent Pod 2 (10.244.2.15)"]
            GW1 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod3["Agent Pod 3 (10.244.3.20)"]

            GW2 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod1
            GW2 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod2
            GW2 -->|L7 per-RPC 调度 / P2C-EWMA / 熔断| Pod3
        end
    end
```

---

## 2. 部署配置全参考 (Configuration Reference)

### 2.1 启动命令行

网关入口编译产物为 `bin/privshield-gateway`：

```bash
# 1. 编译网关产物
make build

# 2. 启动网关
./bin/privshield-gateway

# 3. 使用开发脚本一键启动
bash ./scripts/dev/start-privacy-gateway.sh
```
# =============================================================================
# PrivShield 网关与负载均衡配置
# =============================================================================
gateway:
  # HTTP/REST 监听配置
  rest_host: "0.0.0.0"
  rest_port: 8000
  
  # gRPC 监听配置
  grpc_host: "0.0.0.0"
  grpc_port: 50000
  
  # 负载均衡策略: round_robin | weighted_round_robin | random | weighted_random | least_connections
  strategy: "weighted_round_robin"
  
  # 主动健康检查探针周期 (秒)
  health_check_interval: 5.0

  # 南北向入站 TLS 终结配置
  tls_enabled: true
  tls_cert_file: "/etc/privshield/certs/gateway-server.crt"
  tls_key_file: "/etc/privshield/certs/gateway-server.key"
  tls_ca_file: "/etc/privshield/certs/gateway-ca.crt"  # 若配置，则强制开启客户端 mTLS 双向认证

# 初始静态后端节点列表 (亦可通过 API 动态注册)
backends:
  - http_url: "https://agent-1.internal:8079"
    grpc_address: "agent-1.internal:50051"
    weight: 3
  - http_url: "https://agent-2.internal:8080"
    grpc_address: "agent-2.internal:50052"
    weight: 2
  - http_url: "https://agent-3.internal:8081"
    grpc_address: "agent-3.internal:50053"
    weight: 1
```

### 2.2 环境变量矩阵

环境变量具备高于配置文件的优先级，在 Docker 与 Kubernetes 部署时推荐直接注入：

| 环境变量 | 默认值 | 推荐生产值 | 说明 |
|---|---|---|---|
| `PRIVACY_GATEWAY_CONFIG` | — | `/etc/privshield/gateway.yaml` | YAML 配置文件绝对路径 |
| `GATEWAY_REST_HOST` | `0.0.0.0` | `0.0.0.0` | 网关 HTTP/REST 监听地址 |
| `GATEWAY_REST_PORT` | `8000` | `8000` | 网关 HTTP/REST 监听端口 |
| `GATEWAY_GRPC_HOST` | `0.0.0.0` | `0.0.0.0` | 网关 gRPC 监听地址 |
| `GATEWAY_GRPC_PORT` | `50000` | `50000` | 网关 gRPC 监听端口 |
| `GATEWAY_STRATEGY` | `round_robin` | `least_connections` / `weighted_round_robin` | 负载均衡调度算法 |
| `GATEWAY_HEALTH_INTERVAL` | `5.0` | `5.0` | 主动探针检测周期（秒） |
| `GATEWAY_BACKENDS` | — | 见下方格式 | 后端节点列表（环境变量注册） |
| `GATEWAY_API_KEY` | — | 强随机 32+ 字符 Token | 动态拓扑管理 API 认证密钥（未配置时默认 503 禁用） |
| `GATEWAY_TLS_ENABLED` | `false` | `true` | 是否启用南北向入站 TLS 终结 |
| `GATEWAY_TLS_CERT` | — | `/etc/privshield/certs/gateway.crt` | 网关服务器 TLS 证书路径 |
| `GATEWAY_TLS_KEY` | — | `/etc/privshield/certs/gateway.key` | 网关服务器 TLS 私钥路径 |
| `GATEWAY_TLS_CA` | — | `/etc/privshield/certs/ca.crt` | 客户端验签 CA 路径（配置后强制开启 mTLS） |
| `PRIVACY_GATEWAY_BACKEND_TLS_ENABLED` | `false` | `true`（若后端启用 TLS） | 是否启用网关至后端的东西向 TLS 回源 |
| `PRIVACY_GATEWAY_BACKEND_TLS_CA` | — | `/etc/privshield/certs/backend-ca.crt` | 校验后端证书的 CA 文件（开启回源 TLS 时必填） |
| `PRIVACY_GATEWAY_BACKEND_TLS_CLIENT_CERT`| — | `/etc/privshield/certs/gateway-client.crt` | 回源客户端证书（后端要求 mTLS 时配置） |
| `PRIVACY_GATEWAY_BACKEND_TLS_CLIENT_KEY` | — | `/etc/privshield/certs/gateway-client.key` | 回源客户端私钥（后端要求 mTLS 时配置） |
| `PRIVACY_LOG_LEVEL` | `INFO` | `INFO` / `WARNING` | 网关日志输出级别 |
| `PRIVACY_LOG_FORMAT` | `text` | `json` | 生产环境推荐设置为 `json` 结构化输出 |

> **`GATEWAY_BACKENDS` 格式说明**：
> 多个节点使用英文逗号分隔，每个节点的 HTTP 与 gRPC 地址使用 `|` 隔开：
> ```bash
> export GATEWAY_BACKENDS="https://agent-1:8079|agent-1:50051,https://agent-2:8080|agent-2:50052"
> ```

### 2.3 CLI 启动命令行参数

网关入口模块为 [`engine.gateway.server`](engine/gateway/server.py)：

```bash
# 查看帮助
python -m engine.gateway.server --help

# 常用启动选项
python -m engine.gateway.server \
  --rest-host 0.0.0.0 \
  --rest-port 8000 \
  --grpc-host 0.0.0.0 \
  --grpc-port 50000
```

---

## 3. 双向安全与 TLS 运维实战

### 3.1 生产/测试证书一键签发脚本

创建 `generate_gateway_certs.sh` 快速生成网关所需的测试/自签名双向证书：

```bash
#!/usr/bin/env bash
set -euo pipefail
CERT_DIR="./certs"
mkdir -p "$CERT_DIR"

echo "1. 生成根 CA (Certificate Authority)..."
openssl req -x509 -newkey rsa:4096 -days 3650 -nodes \
  -keyout "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" \
  -subj "/C=CN/ST=Zhejiang/L=Hangzhou/O=PrivShield/OU=Security/CN=PrivShield-Root-CA"

echo "2. 生成网关服务端证书 (Gateway Server Cert)..."
openssl req -newkey rsa:2048 -nodes \
  -keyout "$CERT_DIR/gateway-server.key" -out "$CERT_DIR/gateway-server.csr" \
  -subj "/C=CN/ST=Zhejiang/L=Hangzhou/O=PrivShield/OU=Gateway/CN=gateway.privshield.internal"

cat <<EOF > "$CERT_DIR/gateway-ext.cnf"
subjectAltName = @alt_names
[alt_names]
DNS.1 = gateway.privshield.internal
DNS.2 = localhost
IP.1 = 127.0.0.1
EOF

openssl x509 -req -days 1095 -in "$CERT_DIR/gateway-server.csr" \
  -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
  -out "$CERT_DIR/gateway-server.crt" -extfile "$CERT_DIR/gateway-ext.cnf"

echo "3. 生成客户端 mTLS 证书 (Client Cert)..."
openssl req -newkey rsa:2048 -nodes \
  -keyout "$CERT_DIR/client.key" -out "$CERT_DIR/client.csr" \
  -subj "/C=CN/ST=Zhejiang/L=Hangzhou/O=PrivShield/OU=Client/CN=client.privshield.internal"

openssl x509 -req -days 1095 -in "$CERT_DIR/client.csr" \
  -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
  -out "$CERT_DIR/client.crt"

chmod 600 "$CERT_DIR"/*.key
echo "✅ 证书生成完毕，保存在 $CERT_DIR 目录。"
```

### 3.2 南北向 TLS 终结与 mTLS 客户端验签

在网关启动时配置以下环境变量，实现对外部客户端的 TLS 终结与强双向身份认证：

```bash
export GATEWAY_TLS_ENABLED=true
export GATEWAY_TLS_CERT=/etc/privshield/certs/gateway-server.crt
export GATEWAY_TLS_KEY=/etc/privshield/certs/gateway-server.key
export GATEWAY_TLS_CA=/etc/privshield/certs/ca.crt  # 配置后强制开启客户端证书校验
```

- **HTTP 客户端访问示例 (mTLS)**：
  ```bash
  curl --cacert certs/ca.crt \
       --cert certs/client.crt \
       --key certs/client.key \
       https://127.0.0.1:8000/health
  ```
- **gRPC 客户端访问示例 (Python)**：
  ```python
  import grpc
  from engine import privacy_pb2, privacy_pb2_grpc

  with open("certs/ca.crt", "rb") as f:
      root_certs = f.read()
  with open("certs/client.key", "rb") as f:
      private_key = f.read()
  with open("certs/client.crt", "rb") as f:
      cert_chain = f.read()

  creds = grpc.ssl_channel_credentials(root_certs, private_key, cert_chain)
  channel = grpc.aio.secure_channel("127.0.0.1:50000", creds)
  stub = privacy_pb2_grpc.PrivacyServiceStub(channel)
  ```

### 3.3 东西向网关至后端 TLS 安全回源

当后端 Agent 运行在安全内网且启用了 TLS/mTLS 时，网关必须配置回源凭据以完成握手：

```bash
# 启用回源 TLS 校验
export PRIVACY_GATEWAY_BACKEND_TLS_ENABLED=true
export PRIVACY_GATEWAY_BACKEND_TLS_CA=/etc/privshield/certs/ca.crt

# 若后端 Agent 开启了 mTLS 客户端证书白名单：
export PRIVACY_GATEWAY_BACKEND_TLS_CLIENT_CERT=/etc/privshield/certs/gateway-client.crt
export PRIVACY_GATEWAY_BACKEND_TLS_CLIENT_KEY=/etc/privshield/certs/gateway-client.key
```

> ⚠️ **Fail-Fast 安全机制**：若 `PRIVACY_GATEWAY_BACKEND_TLS_ENABLED=true` 但未设置 `PRIVACY_GATEWAY_BACKEND_TLS_CA` 或 CA 文件不存在，网关在启动和访问节点时将**直接报错拒绝启动**，严防误退化为明文传输。

### 3.4 动态管理端点鉴权与 SSRF 阻断防护

网关的动态注册路由 `/v1/gateway/register` 与 `/v1/gateway/deregister` 支持热更新拓扑。生产环境必须设置强随机密钥：

```bash
export GATEWAY_API_KEY="sk_gw_prod_9f8b7c6d5e4a3b2a10987654321fedcba"
```

- **Fail-Closed 保护**：未配置 `GATEWAY_API_KEY` 时，任何调用一律返回 `503 Service Unavailable`；
- **防 SSRF 机制**：仅允许 `http://` 或 `https://` 协议，非法协议（如 `file://`, `gopher://`, `dict://`）一律返回 `400 Bad Request`。

---

## 4. 动态伸缩与节点生命周期管理

### 4.1 动态注册接口 (`POST /v1/gateway/register`)

向网关节点池动态注册或热更新一个 Agent 计算节点：

```bash
curl -X POST http://127.0.0.1:8000/v1/gateway/register \
  -H "Authorization: Bearer sk_gw_prod_9f8b7c6d5e4a3b2a10987654321fedcba" \
  -H "Content-Type: application/json" \
  -d '{
    "http_url": "http://192.168.1.105:8079",
    "grpc_address": "192.168.1.105:50051",
    "weight": 3
  }'
```

**响应码与含义**：
- `200 OK`：`{"status": "registered"}`（若节点已存在则就地更新权重并重置健康状态）；
- `400 Bad Request`：`http_url` 协议非法；
- `401 Unauthorized`：Token 缺失或比对不匹配；
- `503 Service Unavailable`：网关未启用 `GATEWAY_API_KEY`。

### 4.2 动态注销接口 (`POST /v1/gateway/deregister`)

在 Agent 节点下线、停机维护或缩容时通知网关主动剔除：

```bash
curl -X POST http://127.0.0.1:8000/v1/gateway/deregister \
  -H "Authorization: Bearer sk_gw_prod_9f8b7c6d5e4a3b2a10987654321fedcba" \
  -H "Content-Type: application/json" \
  -d '{
    "http_url": "http://192.168.1.105:8079",
    "grpc_address": "192.168.1.105:50051"
  }'
```

### 4.3 Agent 启动/退出生命周期自动注册脚本示例

可将以下逻辑集成至 Agent 启动脚本或容器入口（Entrypoint）中：

```bash
#!/usr/bin/env bash
GATEWAY_URL="http://gateway.internal:8000"
GATEWAY_KEY="sk_gw_prod_9f8b7c6d5e4a3b2a10987654321fedcba"
MY_HTTP="http://$(hostname -I | awk '{print $1}'):8079"
MY_GRPC="$(hostname -I | awk '{print $1}'):50051"

# 1. 启动 Agent 主服务并在后台运行
python -m engine.server &
AGENT_PID=$!

# 2. 等待本地 Agent 健康就绪
while ! curl -s "http://127.0.0.1:8079/health" | grep -q '"status":"ok"'; do
    sleep 0.5
done

# 3. 向网关注册本节点
echo "向网关注册节点: $MY_HTTP / $MY_GRPC"
curl -s -X POST "$GATEWAY_URL/v1/gateway/register" \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"http_url\": \"$MY_HTTP\", \"grpc_address\": \"$MY_GRPC\", \"weight\": 2}"

# 4. 捕获退出信号，注销后再退出
cleanup() {
    echo "正在从网关注销节点..."
    curl -s -X POST "$GATEWAY_URL/v1/gateway/deregister" \
      -H "Authorization: Bearer $GATEWAY_KEY" \
      -H "Content-Type: application/json" \
      -d "{\"http_url\": \"$MY_HTTP\", \"grpc_address\": \"$MY_GRPC\"}"
    kill -TERM "$AGENT_PID" 2>/dev/null || true
    wait "$AGENT_PID"
}
trap cleanup SIGTERM SIGINT

wait "$AGENT_PID"
```

---

## 5. 分布式共享隐私预算记账运维

### 5.1 共享存储卷挂载与环境配置

在多节点集群中，各 Agent 节点通过 `PRIVACY_BUDGET_DB` 共享 SQLite 数据库实现强一致记账：

```bash
# 所有 Agent 容器挂载同一共享卷（NFS / K8s ReadWriteMany PVC / 本地挂载目录）
export PRIVACY_BUDGET_DB="/mnt/shared/privshield/privacy_budget.db"
```

SQLite `BEGIN IMMEDIATE` 排他事务机制保障多实例并发更新时不会产生 Race Condition 或预算超扣。

### 5.2 预算数据库定时备份与恢复

生产环境推荐使用项目内置脚本 [`scripts/prod/backup_privacy_budget.sh`](scripts/prod/backup_privacy_budget.sh)：

```bash
# 1. 手动执行备份
bash ./scripts/prod/backup_privacy_budget.sh

# 2. 配置 Crontab 定时任务（每天凌晨 2 点执行）
0 2 * * * /bin/bash /opt/PrivShield/scripts/prod/backup_privacy_budget.sh >> /var/log/budget_backup.log 2>&1
```

**恢复操作**：
```bash
# 停止所有 Agent 节点
# 将备份文件拷贝覆盖
cp /data/backups/privacy_budget_20260819_020000.db /mnt/shared/privshield/privacy_budget.db
# 检查数据库完整性
sqlite3 /mnt/shared/privshield/privacy_budget.db "PRAGMA integrity_check;"
# 重启 Agent 节点
```

---

## 6. 多环境部署实战演练

### 6.1 裸机与虚拟机部署 (Systemd 服务托管)

在 `/etc/systemd/system/privshield-gateway.service` 创建服务文件：

```ini
[Unit]
Description=PrivShield API Gateway & Load Balancer
After=network.target

[Service]
Type=simple
User=privshield
Group=privshield
WorkingDirectory=/opt/privshield
Environment=GATEWAY_HOST=0.0.0.0
Environment=GATEWAY_PORT=8000
Environment=GATEWAY_GRPC_PORT=50000
Environment=GATEWAY_STRATEGY=p2c
Environment=GATEWAY_BACKENDS=127.0.0.1:8079,127.0.0.1:8080
Environment=PRIVACY_LOG_LEVEL=INFO
ExecStart=/opt/privshield/bin/privshield-gateway
Restart=always
RestartSec=3s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

```bash
# 重新加载并启动
sudo systemctl daemon-reload
sudo systemctl enable --now privshield-gateway
sudo systemctl status privshield-gateway
```

### 6.2 Docker & Docker Compose 生产编排

创建 `docker-compose.gateway.yml`：

```yaml
version: '3.8'

networks:
  privshield-net:
    driver: bridge

services:
  # Agent 计算节点 1
  agent-worker-1:
    image: privshield-agent:10.0.0
    restart: always
    environment:
      - PRIVACY_REST_PORT=8079
      - PRIVACY_GRPC_PORT=50051
      - PRIVACY_LOG_LEVEL=INFO
    networks:
      - privshield-net

  # Agent 计算节点 2
  agent-worker-2:
    image: privshield-agent:10.0.0
    restart: always
    environment:
      - PRIVACY_REST_PORT=8079
      - PRIVACY_GRPC_PORT=50051
      - PRIVACY_LOG_LEVEL=INFO
    networks:
      - privshield-net

  # 网关与负载均衡器
  gateway:
    image: privshield-agent:10.0.0
    restart: always
    command: ["/app/privshield-gateway"]
    environment:
      - GATEWAY_HOST=0.0.0.0
      - GATEWAY_PORT=8000
      - GATEWAY_GRPC_PORT=50000
      - GATEWAY_STRATEGY=p2c
      - GATEWAY_BACKENDS=agent-worker-1:8079,agent-worker-2:8079
      - PRIVACY_LOG_LEVEL=INFO
    ports:
      - "8000:8000"
      - "50000:50000"
    networks:
      - privshield-net
    depends_on:
      - agent-worker-1
      - agent-worker-2
```

```bash
# 启动网关与集群
docker compose -f docker-compose.gateway.yml up -d
```

### 6.3 Kubernetes 生产部署（网关与 K8s 双层协同实战）

本节提供标准双层协同模式下的完整 Kubernetes 编排清单与运维标准操作。

#### 6.3.1 完整双层协同架构编排清单 (`privshield-k8s-two-tier.yaml`)

```yaml
# =============================================================================
# 1. 命名空间与鉴权 Secret
# =============================================================================
apiVersion: v1
kind: Namespace
metadata:
  name: privshield
---
apiVersion: v1
kind: Secret
metadata:
  name: gateway-secrets
  namespace: privshield
type: Opaque
stringData:
  api-key: "sk_gw_prod_9f8b7c6d5e4a3b2a10987654321fedcba"
---
# =============================================================================
# 2. 外部访问入口 Ingress (南北向 L7 接入与 SSL 卸载)
# =============================================================================
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: privshield-ingress
  namespace: privshield
  annotations:
    kubernetes.io/ingress.class: "nginx"
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
    nginx.ingress.kubernetes.io/proxy-body-size: "64m"
spec:
  rules:
    - host: gateway.privshield.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: privshield-gateway-svc
                port:
                  number: 8000
---
# =============================================================================
# 3. PrivShield Gateway (网关调度层 Deployment & Service)
# =============================================================================
apiVersion: v1
kind: Service
metadata:
  name: privshield-gateway-svc
  namespace: privshield
spec:
  type: ClusterIP
  selector:
    app: privshield-gateway
  ports:
    - name: http
      port: 8000
      targetPort: 8000
    - name: grpc
      port: 50000
      targetPort: 50000
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: privshield-gateway
  namespace: privshield
spec:
  replicas: 2
  selector:
    matchLabels:
      app: privshield-gateway
  template:
    metadata:
      labels:
        app: privshield-gateway
    spec:
      containers:
        - name: gateway
          image: privshield-agent:10.0.0
          imagePullPolicy: IfNotPresent
          command: ["/app/privshield-gateway"]
          env:
            - name: GATEWAY_HOST
              value: "0.0.0.0"
            - name: GATEWAY_PORT
              value: "8000"
            - name: GATEWAY_GRPC_PORT
              value: "50000"
            - name: GATEWAY_STRATEGY
              value: "p2c"
            - name: GATEWAY_BACKENDS
              value: "privshield-agent-headless:8079"
            - name: PRIVACY_LOG_LEVEL
              value: "INFO"
          ports:
            - containerPort: 8000
              name: http
            - containerPort: 50000
              name: grpc
          resources:
            requests:
              cpu: "500m"
              memory: "512Mi"
            limits:
              cpu: "2000m"
              memory: "2Gi"
          readinessProbe:
            httpGet:
              path: /health
              port: 8000
            initialDelaySeconds: 3
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /health
              port: 8000
            initialDelaySeconds: 5
            periodSeconds: 10
---
# =============================================================================
# 4. PrivShield Agent (计算节点 Headless Service & Deployment)
# =============================================================================
# Headless Service (clusterIP: None) 允许网关直接解析后端每个 Pod 的独立 IP
apiVersion: v1
kind: Service
metadata:
  name: privshield-agent-headless
  namespace: privshield
spec:
  clusterIP: None
  selector:
    app: privshield-agent
  ports:
    - name: http
      port: 8079
    - name: grpc
      port: 50051
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: privshield-agent
  namespace: privshield
spec:
  replicas: 3
  selector:
    matchLabels:
      app: privshield-agent
  template:
    metadata:
      labels:
        app: privshield-agent
    spec:
      containers:
        - name: agent
          image: privshield:1.8.0
          imagePullPolicy: IfNotPresent
          env:
            - name: PRIVACY_REST_PORT
              value: "8079"
            - name: PRIVACY_GRPC_PORT
              value: "50051"
            - name: PRIVACY_LOG_FORMAT
              value: "json"
            - name: PRIVACY_BUDGET_DB
              value: "/data/budget/privacy_budget.db"
          ports:
            - containerPort: 8079
              name: http
            - containerPort: 50051
              name: grpc
          resources:
            requests:
              cpu: "1000m"
              memory: "2Gi"
            limits:
              cpu: "4000m"
              memory: "8Gi"
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8079
            initialDelaySeconds: 5
            periodSeconds: 5
          volumeMounts:
            - name: shared-budget
              mountPath: /data/budget
      volumes:
        - name: shared-budget
          persistentVolumeClaim:
            claimName: privshield-budget-pvc
```

#### 6.3.2 部署、弹性扩缩容与连通性验证 SOP

1. **一键应用编排清单**：
   ```bash
   kubectl apply -f privshield-k8s-two-tier.yaml
   ```

2. **验证 Pod 与 Headless DNS 解析**：
   ```bash
   # 检查 Pod 运行状态
   kubectl get pods -n privshield -o wide

   # 在网关 Pod 内验证 Headless Service DNS 解析出多个 Pod IP
   kubectl exec -it deployment/privshield-gateway -n privshield -- \
     python3 -c "import socket; print(socket.gethostbyname_ex('privshield-agent-headless.privshield.svc.cluster.local'))"
   ```

3. **动态注册或环境变量注入后端**：
   ```bash
   # 将 Headless DNS 记录注入网关，或通过各个 Agent Pod 启动钩子向网关 POST /v1/gateway/register 注册自身
   ```

4. **Agent 计算节点动态水平扩缩容（HPA / Manual Scale）**：
   ```bash
   # 伸缩 Agent 副本数为 5
   kubectl scale deployment/privshield-agent -n privshield --replicas=5

   # 网关自动在新节点就绪后将其纳入 least_connections 调度池，无须重启网关
   ```

5. **连通性与负载均衡测试**：
   ```bash
   # 通过 Ingress 域名请求脱敏接口
   curl -X POST http://gateway.privshield.example.com/v1/privacy/mask \
     -H "Content-Type: application/json" \
     -d '{"field_name": "mobile", "value": "13812345678", "context": ""}'
   ```

---

## 7. 全链路可观测性、监控指标与告警

### 7.1 Prometheus 抓取配置 (`prometheus.yml`)

将网关加入 Prometheus 监控抓取作业：

```yaml
scrape_configs:
  - job_name: "privshield-gateway"
    metrics_path: "/metrics"
    scrape_interval: 15s
    static_configs:
      - targets: ["127.0.0.1:8000"]
```

### 7.2 生产核心 PromQL 监控查询

| 观测维度 | PromQL 查询语句 | 说明 |
|---|---|---|
| **网关 QPS 吞吐量** | `sum(rate(privacy_gateway_requests_total[1m])) by (protocol, status)` | 按协议和状态码统计每秒转发量 |
| **网关 P99 转发延迟** | `histogram_quantile(0.99, sum(rate(privacy_gateway_latency_seconds_bucket[5m])) by (le))` | 观测网关至后端的 P99 延迟分布 |
| **可用健康节点数** | `privacy_gateway_healthy_nodes` | 监控当前处于可用状态的 Agent 数量 |
| **网关重试率** | `sum(rate(privacy_gateway_retries_total[5m])) / sum(rate(privacy_gateway_requests_total[5m]))` | 监控故障转移与重试发生比例 |
| **网关 5xx 错误率** | `sum(rate(privacy_gateway_requests_total{status=~"5.."}[5m])) / sum(rate(privacy_gateway_requests_total[5m]))` | 监控网关层报错率 |

### 7.3 Prometheus 告警规则矩阵

对应项目告警规则文件 [`deploy/prometheus/alerts.yml`](deploy/prometheus/alerts.yml)：

```yaml
groups:
  - name: engine.gateway
    rules:
      # 1. 网关无可用健康节点 (致命告警)
      - alert: GatewayNoHealthyNodes
        expr: privacy_gateway_healthy_nodes == 0
        for: 1m
        labels:
          severity: critical
          service: PrivShield
        annotations:
          summary: "网关可用后端节点降为 0"
          description: "PrivShield 网关可用健康节点数为 0 已超过 1 分钟，所有外部请求将失败！"
          runbook_url: "https://github.com/fengzhizi319/PrivShield-go/docs/gateway_balancer/ops.md#故障-1503-no-healthy-backend-nodes-available--grpc-unavailable"

      # 2. 网关可用节点容量降级 (警告)
      - alert: GatewayDegradedCapacity
        expr: privacy_gateway_healthy_nodes < 2
        for: 5m
        labels:
          severity: warning
          service: PrivShield
        annotations:
          summary: "网关处于降级容量运行"
          description: "健康后端节点数小于 2 持续超过 5 分钟，存在单点风险。"

      # 3. 网关 5xx 错误率过高 (致命告警)
      - alert: HighGatewayErrorRate
        expr: |
          sum(rate(privacy_gateway_requests_total{status=~"5.."}[5m]))
          /
          sum(rate(privacy_gateway_requests_total[5m])) > 0.05
        for: 5m
        labels:
          severity: critical
          service: PrivShield
        annotations:
          summary: "网关 5xx 错误率超过 5%"
          description: "过去 5 分钟内网关 5xx 错误率持续高于 5%。"

      # 4. 网关故障重试率异常 (警告)
      - alert: HighGatewayRetryRate
        expr: |
          sum(rate(privacy_gateway_retries_total[5m]))
          /
          sum(rate(privacy_gateway_requests_total[5m])) > 0.10
        for: 5m
        labels:
          severity: warning
          service: PrivShield
        annotations:
          summary: "网关重试率高于 10%"
          description: "后端节点可能存在网络抖动或崩溃，网关正在频繁触发故障转移重试。"
```

### 7.4 结构化 JSON 日志集中采集与检索

设置 `PRIVACY_LOG_FORMAT=json` 后，网关输出结构化日志。常用日志过滤示例（Loki / jq）：

```bash
# 检索所有重试告警事件
cat /var/log/privshield/gateway.log | jq 'select(.message | contains("retrying"))'

# 检索节点健康状态变更记录
cat /var/log/privshield/gateway.log | jq 'select(.message == "Node status changed")'
```

---

## 8. 生产故障排查手册与应急预案 (Runbook)

> **快速故障速查表**：
>
> | 故障现象 | 可能原因 | 快速处理 |
> |---|---|---|
> | **HTTP 502 全量失败** | 所有后端节点不可达 | 检查 `privacy_gateway_healthy_nodes` 是否为 0；查看日志 `error` 字段 |
> | **503 No healthy backend** | 节点池为空或全部熔断/冷却 | 确认已配置 `GATEWAY_BACKENDS`；检查被动冷却期（5s）是否已过 |
> | **节点反复上下线抖动** | 健康检查超时或后端响应慢 | 检查 `GATEWAY_HEALTH_INTERVAL`；确认后端 `/health` 延迟 < 2s |
> | **熔断器长期 Open** | 后端持续 5xx 或网络故障 | 查看后端应用日志；检查 `circuit_breaker` 状态；临时重启后端 Pod |
> | **gRPC UNAVAILABLE** | gRPC 通道异常或后端未启动 | 确认 gRPC 端口可达；检查回源 TLS 配置；查看 `last_error` 日志 |
> | **TLS 握手失败** | 证书不匹配或 CA 未配置 | 确认证书文件存在且格式正确；检查 CA 路径；验证证书有效期 |
> | **管理端点 503** | 未配置 `GATEWAY_API_KEY` | 显式设置 `GATEWAY_API_KEY` 环境变量并重启网关 |

### 故障 1：`503 No healthy backend nodes available` / `gRPC UNAVAILABLE`

- **现象**：客户端请求全部返回 HTTP 503 或 gRPC `StatusCode.UNAVAILABLE`；Prometheus 告警 `GatewayNoHealthyNodes` 触发。
- **根因分析**：
  1. 初始配置的后端节点列表地址错误或离线；
  2. 所有后端节点均在健康检查探针中失败（HTTP `/health` 或 gRPC `Health` 未返回 `"status": "ok"`）；
  3. 后端连续故障导致全部节点触发熔断器（Circuit Breaker 处于 `Open` 状态）。
- **排查与恢复步骤**：
  1. 检查网关日志定位节点状态：
     ```bash
     grep "Node status changed" /var/log/privshield/gateway.log | tail -n 20
     ```
  2. 手动在网关宿主机上验证后端 Agent 探针连通性：
     ```bash
     curl -v http://<backend_ip>:8079/health
     python3 -c "import socket; s=socket.socket(); s.settimeout(2); s.connect(('<backend_ip>', 50051)); print('gRPC port OK'); s.close()"
     ```
  3. 若后端 Agent 异常崩溃，重启后端 Agent 进程；
  4. 后端恢复后，网关在下一个探针周期（默认 5s）或动态重注册时会自动复位熔断器并恢复流量。

---

### 故障 2：`502 Bad Gateway: all 3 backend retry attempts failed`

- **现象**：客户端收到 HTTP 502 报错；`privacy_gateway_retries_total` 指标飙升。
- **根因分析**：
  1. 网关发起的请求在连续 3 次尝试（含故障转移节点）中均遭遇 TCP 连接断开或读写超时；
  2. 后端计算负载过重（如大批次差分隐私或多模态大模型分类），导致单次请求耗时超过网关的 30 秒超时阈值。
- **排查与恢复步骤**：
  1. 检查网关内部结构化日志中的 `last_error` 字段（网关不会向客户端透露内网 URL，但完整记录于日志中）：
     ```bash
     grep "HTTP proxy request failed after all retries" /var/log/privshield/gateway.log
     ```
  2. 若原因为连接超时（`ReadTimeout`），排查后端计算节点的 GPU/CPU 占用率；
  3. 若为单次请求数据量过大，调大客户端批次划分粒度或调优后端推理超时配置。

---

### 故障 3：管理端点 `503 Gateway management API is disabled` / `401 Unauthorized`

- **现象**：调用 `/v1/gateway/register` 或 `/v1/gateway/deregister` 返回 503 或 401。
- **根因分析**：
  - 返回 503：网关未设置环境变量 `GATEWAY_API_KEY`（Fail-Closed 保护）；
  - 返回 401：请求未携带 `Authorization: Bearer <KEY>` 头或 Token 错误。
- **恢复步骤**：
  1. 检查网关启动环境变量，确保已注入 `GATEWAY_API_KEY`；
  2. 客户端请求中加入正确的 Bearer Token。

---

### 故障 4：gRPC 消息体超限错误 (`RESOURCE_EXHAUSTED`)

- **现象**：传输大表或高清图像时 gRPC 返回 `RESOURCE_EXHAUSTED: Received message larger than max (xxx vs 4194304)`。
- **根因分析**：
  - 传统 gRPC 默认消息上限为 4 MiB。虽然 `engine.gateway` 已调优至 64 MiB，但若客户端自身未配置 64 MiB 缓冲区，则客户端会在接收端抛错。
- **恢复步骤**：
  - 确保客户端与网关均配置 `grpc.max_receive_message_length` 和 `grpc.max_send_message_length` 为 `64 * 1024 * 1024` (64 MiB)。

---

### 故障 5：TLS 握手失败 / `SSLError` / 证书过期

- **现象**：客户端报错 `SSL: CERTIFICATE_VERIFY_FAILED` 或网关回源报错 `回源 TLS CA 文件不存在`。
- **排查与恢复步骤**：
  1. 运行诊断脚本检查证书剩余有效期：
     ```bash
     ./scripts/prod/prod_health_check.sh --tls --cert-file /etc/privshield/certs/gateway-server.crt
     ```
  2. 检查域名与 SAN (Subject Alternative Name) 是否匹配：
     ```bash
     openssl x509 -text -noout -in /etc/privshield/certs/gateway-server.crt | grep -A 2 "Subject Alternative Name"
     ```
  3. 若开启回源 TLS，确保证书文件具有当前运行用户的读取权限（`chmod 644 ca.crt`）。

---

### 故障 6：连接池耗尽 / 跨事件循环 (Event Loop) 异常

- **现象**：高并发下出现 `OSError: Cannot assign requested address` 或日志中出现客户端连接重建记录。
- **根因分析与调优**：
  1. 检查系统 TCP TIME_WAIT 套接字回收：
     ```bash
     sysctl -w net.ipv4.tcp_tw_reuse=1
     ```
  2. 网关已内置单例连接池（Keep-Alive 100 / Max Connections 500）。若 QPS 超过 5000，建议在同一主机水平拉起多个网关实例，前置 Nginx / LVS 进行端口分流。

---

### 故障 7：Kubernetes 环境下 gRPC 流量倾斜与单 Pod 负载过高 (Single-Pod Pinning)

- **现象**：在 K8s 中部署了 5 个 Agent Pod，但监控显示所有 gRPC 请求始终只落在一个 Pod 上，导致其 CPU 跑满，其余 4 个 Pod 处于闲置状态。
- **根因分析**：
  - gRPC 基于 HTTP/2 协议，默认在同一个 TCP 长连接上多路复用所有 RPC 调用；
  - 若网关后端直接配置为普通 K8s ClusterIP Service（如 `http://agent-svc:8079|agent-svc:50051`），网关建立连接时由 K8s Kube-Proxy（L4 传输层）仅做了一次 TCP 握手调度，后续所有 RPC 请求都被“钉”在同一个 Pod 上。
- **恢复步骤**：
  1. 将 Agent Service 改建为 **Headless Service**（设置 `spec.clusterIP: "None"`）；
  2. 在网关中启用 `least_connections` 调度策略；
  3. 网关直接与各个 Agent Pod 建立独立 Channel，并在应用层执行 per-RPC 级调度，彻底消除负载倾斜。

---

## 9. 生产健康巡检与诊断工具

项目提供了开箱即用的生产全链路健康巡检脚本 [`scripts/prod/prod_health_check.sh`](scripts/prod/prod_health_check.sh)：

```bash
# 1. 基础巡检 (HTTP/gRPC/Metrics/DB)
bash ./scripts/prod/prod_health_check.sh \
  --rest-host 127.0.0.1 \
  --rest-port 8000 \
  --grpc-host 127.0.0.1 \
  --grpc-port 50000 \
  --db-path /data/privacy_budget.db

# 2. 启用 TLS 证书过期诊断
bash ./scripts/prod/prod_health_check.sh \
  --rest-host gateway.privshield.internal \
  --rest-port 8000 \
  --tls \
  --cert-file /etc/privshield/certs/gateway-server.crt
```

**诊断输出示例**：
```text
============================================================================
🛡️  【生产模式】PrivShield 生产环境全链路健康巡检
============================================================================
目标配置:
  • REST 地址 : http://127.0.0.1:8000
  • gRPC 地址 : 127.0.0.1:50000
  • TLS 模式  : false
  • 预算数据库 : /data/privacy_budget.db
────────────────────────────────────────────────────────────────────────────
[1/5] REST API 与探针检查
  [PASS] 基础健康探针 (/health) -> HTTP 200 (2ms)
  [PASS] K8s 存活探针 (/livez) -> HTTP 200 (1ms)
  [PASS] K8s 就绪探针 (/readyz) -> HTTP 200 (1ms)

[2/5] 生产可观测性 Prometheus Metrics 指标检查
  [PASS] Prometheus Metrics 端点响应正常 (http://127.0.0.1:8000/metrics)

[3/5] gRPC 核心服务连通性检查
  [PASS] gRPC 端口可用 (127.0.0.1:50000)

[4/5] TLS 证书安全期巡检
  [PASS] TLS 证书有效: 剩余 365 天过期

[5/5] 隐私预算存储 (SQLite) 健康度巡检
  [PASS] 隐私预算数据库状态正常 (文件大小: 64K, 完整性校验通过)

============================================================================
巡检结果汇总:
  • 检查总项 : 7
  • 通过项目 : 7
  • 警告项目 : 0
  • 失败项目 : 0

✅ 生产环境健康检查全部通过！服务运行稳健。
============================================================================
```

---

## 10. 生产上线前检查清单 (Production Readiness Checklist)

| 序号 | 检查项目 | 达标标准 | 责任人确认 |
|:---:|---|---|:---:|
| 1 | **网络与端口** | REST (8000) 与 gRPC (50000) 端口已在防火墙/安全组正确开放 | [ ] |
| 2 | **文件句柄限制** | 宿主机/容器 `ulimit -n` 大于 65535，防止并发套接字耗尽 | [ ] |
| 3 | **TLS 证书安全** | 服务器证书有效期大于 30 天，已配置自动续期，回源 CA 正确配置 | [ ] |
| 4 | **动态管理鉴权** | 必须显式设置 `GATEWAY_API_KEY`（32+ 位高强度随机串） | [ ] |
| 5 | **预算共享存储** | `PRIVACY_BUDGET_DB` 挂载于共享持久卷，已配置每日定时备份 | [ ] |
| 6 | **可观测性接入** | Prometheus 已配置抓取 `/metrics`，Grafana 看板与 Alertmanager 告警规则生效 | [ ] |
| 7 | **结构化日志** | `PRIVACY_LOG_FORMAT=json`，日志已接入集中式收集平台 (ELK/Loki) | [ ] |
| 8 | **熔断与重试** | 节点连续失败阈值 (5) 与恢复窗口 (30s) 已通过集成测试验证 | [ ] |
| 9 | **K8s 双层协同** | Agent Service 已配置为 Headless（`clusterIP: None`），彻底消除 gRPC 长连接倾斜 | [ ] |
| 10 | **全链路健康巡检** | 执行 `./scripts/prod/prod_health_check.sh` 退出码为 0 | [ ] |

---

## 11. 容量规划与性能基准

### 11.1 单网关实例性能基线

| 指标 | 参考值 | 测试条件 |
|---|---|---|
| HTTP 代理吞吐量 | ~2000 RPS | 1 后端节点，简单 JSON 响应，`round_robin` 策略 |
| gRPC 代理吞吐量 | ~3000 RPS | 1 后端节点，小消息体 (<1KB)，`round_robin` 策略 |
| 代理附加延迟 (P50) | < 1ms | 网关本身处理开销（不含后端响应时间） |
| 代理附加延迟 (P99) | < 5ms | 含连接池获取、Header 清洗、指标记录 |
| 内存基线 | ~80–120 MB | 空闲状态，10 后端节点，无在途请求 |
| 最大并发连接 | 500 | `max_connections` 默认上限，超出后排队 |

### 11.2 请求超时与连接池参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| HTTP 转发超时 | 30s | `httpx.Timeout(30.0)`，含 connect/read/write/pool 四维度 |
| gRPC 转发超时 | 30s | `stub_method(request, timeout=30.0)` |
| 健康检查超时 | 2s | 防止探测挂死影响巡检周期 |
| `max_keepalive_connections` | 100 | 长连接保持上限 |
| `max_connections` | 500 | 并发连接绝对上限 |
| `GRPC_MAX_MESSAGE_BYTES` | 64 MiB | gRPC 单消息体积上限 |

### 11.3 扩展建议

| 场景 | 建议 |
|---|---|
| 后端节点数 > 20 | 考虑部署多个网关实例，配合 K8s Ingress 分发 |
| 并发请求 > 500 | 调高 `max_connections` 或部署多网关实例分摊流量 |
| 大消息传输频繁 (>10MB) | 确认 `GRPC_MAX_MESSAGE_BYTES` (64MiB) 足够，必要时调高 |
| 混合 CPU/GPU 后端 | 采用 `weighted_round_robin` 或 `least_connections` 策略，GPU 节点配置高权重 |
| 高可用要求 > 99.9% | 至少部署 2 个网关实例 + 3 个后端 Agent，启用熔断与双协议探针 |

### 11.4 配置优先级链

网关配置遵循四级覆盖优先级（从高到低）：

```
CLI 命令行参数  >  环境变量 (GATEWAY_*)  >  YAML 配置文件  >  内置默认值
```

- **内置默认值**：`load_config()` 初始化时硬编码（REST `0.0.0.0:8000`、gRPC `0.0.0.0:50000`、策略 `round_robin`）；
- **YAML 配置文件**：通过 `PRIVACY_GATEWAY_CONFIG` 环境变量指定路径，`yaml.safe_load` 解析后合并入默认配置；
- **环境变量**：`GATEWAY_REST_HOST`、`GATEWAY_STRATEGY` 等逐一覆盖，`GATEWAY_BACKENDS` 解析后端列表字符串；
- **CLI 参数**：`argparse` 解析的命令行参数具有最高优先级，仅在显式传入时覆盖。

### 11.5 K8s 优雅停机与 `preStop` Hook 协同

在 Kubernetes 环境中，网关的优雅停机需要与 K8s 生命周期钩子协同配合：

```yaml
# Gateway Deployment 推荐配置
spec:
  terminationGracePeriodSeconds: 30   # K8s 层面总排空窗口
  containers:
    - name: gateway
      lifecycle:
        preStop:
          exec:
            # 先 sleep 5s，等待 Ingress/iptables 规则刷新完毕，
            # 避免在排空期间仍有新流量打入即将终止的 Pod。
            command: ["/bin/sh", "-c", "sleep 5"]
```

停机时序协同：
1. K8s 发送 `SIGTERM` → 网关捕获并触发 `finally` 清理流程；
2. 同时执行 `preStop` hook（`sleep 5`），在此期间网关仍在处理在途请求；
3. 网关 `grpc_server.stop(grace=1.0)` 拒绝新请求并排空在途 gRPC；
4. `balancer.close_all()` 关闭所有后端通道；
5. `preStop` 结束后 K8s 发送 `SIGKILL` 强杀（若仍未退出）。