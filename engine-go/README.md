# PrivShield Go Engine

PrivShield Go 原生隐私计算引擎，提供高性能双协议（REST + gRPC）服务。

## 架构

```
engine-go/
├── cmd/
│   ├── privshield-agent/     # Agent 主入口 (REST :8079 + gRPC :50051)
│   │   └── main.go
│   └── privshield-gateway/   # 网关与反向代理入口 (REST :8000 + gRPC :50000)
│       └── main.go
├── internal/
│   ├── dynclassification/    # 三层动态分类分级漏斗 (AC 自动机 + ONNX NER + 熔断器 LLM)
│   ├── gateway/              # P2C-EWMA 负载均衡器、BufferPool 零分配 HTTP 反向代理与 gRPC 透明流代理
│   ├── grpcserver/           # gRPC Servicer (44 个强类型 RPC 接口) 与 RawCodec 统一分发
│   ├── imageredact/          # DICOM 二进制重构脱敏与图片沙箱防逃逸
│   ├── observability/        # log/slog 结构化日志、Prometheus 指标、分布式追踪
│   ├── profile/              # 领域分类规则与策略 Profile 动态加载器
│   ├── rest/                 # REST 路由 (Gin)、pprof、K8s readyz/healthz
│   ├── security/             # mTLS CN 白名单热重载、32分片令牌桶限流、常量时间认证
│   └── service/              # PrivacyService 统一编排、文件脱敏 (CSV/JSON/XLSX)、预算集成
└── go.mod

privacy-go-sdk/               # 纯 Go 零依赖无状态隐私计算数学原语库
├── masking/                  # 字段掩码（国密 SM3/SM4、身份证、手机、银行卡等）
├── dp/                       # 差分隐私（Laplace / Gaussian / 自适应截断）
├── ldp/                      # 本地差分隐私（二值/多分类多核并发扰动）
├── kano/                     # K-匿名（Mondrian KD-tree + L-多样性）
├── qol/                      # 查询混淆（Fisher-Yates 语义置乱）
├── medical/                  # 医疗数据流水线 (多核并发分块, 医保19 / 康养27)
├── budget/                   # 无锁原子隐私预算会计 (无锁 CAS 循环, 原子回滚)
└── go.mod
```

## 前置要求

- Go 1.25+
- 参考 [Go 安装指南](https://go.dev/doc/install)

### macOS 安装

```bash
# 使用 Homebrew
brew install go

# 验证安装
go version
```

### Linux 安装

```bash
# Ubuntu/Debian
sudo apt-get install golang-go

# CentOS/RHEL
sudo yum install golang

# 或使用官方二进制包
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

## 构建与运行

### 编译

```bash
cd engine-go

# 编译 Agent 与 Gateway 二进制
go build -o ../bin/privshield-agent ./cmd/privshield-agent
go build -o ../bin/privshield-gateway ./cmd/privshield-gateway
```

### 运行

#### 1. 启动 Agent 计算节点 (REST :8079 + gRPC :50051)

```bash
# 默认配置启动
./bin/privshield-agent

# 自定义端口与日志级别
PRIVACY_REST_PORT=8080 PRIVACY_GRPC_PORT=50052 PRIVACY_LOG_LEVEL=DEBUG ./bin/privshield-agent
```

#### 2. 启动 Gateway 反向代理与负载均衡器 (REST :8000 + gRPC :50000)

```bash
# 代理到后端 Agent 节点（支持多个地址逗号分隔，P2C-EWMA 自适应调度）
GATEWAY_BACKENDS="127.0.0.1:8079" GATEWAY_PORT=8000 GATEWAY_GRPC_PORT=50000 ./bin/privshield-gateway
```

### 测试

```bash
# 测试隐私原语库
cd privacy-go-sdk
go test ./... -v

# 测试引擎
cd engine-go
go test ./... -v
```

## API 端点

### REST API (默认端口 8079)

#### 健康检查
```bash
curl http://localhost:8079/health
```

#### 字段掩码
```bash
curl -X POST http://localhost:8079/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{
    "field": "id_card",
    "value": "110101199001011234",
    "type": "id_card"
  }'
```

支持的 `type`：`id_card`, `phone`, `bank_card`, `name`, `email`, `address`

#### 差分隐私 - 噪声计数
```bash
curl -X POST http://localhost:8079/v1/privacy/dp/noisy_count \
  -H "Content-Type: application/json" \
  -d '{
    "count": 100,
    "epsilon": 1.0
  }'
```

#### 差分隐私 - 噪声求和
```bash
curl -X POST http://localhost:8079/v1/privacy/dp/noisy_sum \
  -H "Content-Type: application/json" \
  -d '{
    "values": [1.0, 2.0, 3.0, 4.0, 5.0],
    "epsilon": 1.0,
    "sensitivity": 5.0
  }'
```

#### 动态分类
```bash
curl -X POST http://localhost:8079/v1/privacy/classify/field \
  -H "Content-Type: application/json" \
  -d '{
    "records": [
      {"id_card": "110101199001011234", "phone": "13812345678"}
    ]
  }'
```

#### 预算查询
```bash
curl http://localhost:8079/v1/privacy/budget
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PRIVACY_REST_HOST` | `0.0.0.0` | REST 监听地址 |
| `PRIVACY_REST_PORT` | `8079` | REST 监听端口 |
| `PRIVACY_GRPC_HOST` | `0.0.0.0` | gRPC 监听地址 |
| `PRIVACY_GRPC_PORT` | `50051` | gRPC 监听端口 |
| `PRIVACY_LOG_LEVEL` | `INFO` | 日志级别（DEBUG/INFO/WARN/ERROR） |

## 实现状态

### Phase 1（当前）
- [x] `privacy-go-sdk` 隐私原语库
  - [x] `masking/` 字段掩码
  - [x] `dp/` 差分隐私
  - [x] `ldp/` 本地差分隐私
  - [x] `kano/` K-匿名
  - [x] `qol/` 查询混淆
  - [x] `budget/` 隐私预算会计
- [x] `engine-go` 引擎骨架
  - [x] AC 自动机规则引擎（Layer 1）
  - [x] REST API 服务器（Gin）
  - [x] 可观测性基础设施
- [x] 单元测试

### Phase 2（计划中）
- [ ] gRPC 服务器实现
- [ ] Layer 2 Small-NER（ONNX Runtime）
- [ ] Layer 3 LLM/VLM 仲裁
- [ ] 医疗数据流水线
- [ ] Docker 镜像构建
- [ ] K8s 部署清单

## 性能目标

参考设计文档 §11.4：
- REST QPS ≥ 10,000（4 核）
- gRPC QPS ≥ 30,000（4 核）
- P99 延迟 < 50ms（掩码操作）
- 内存占用 < 200MB（空载）

## 文档

- [架构设计](../docs/archive/go_engine_architecture_and_ner_cuda_design.md)
- [隐私原语 API](./privacy-go-sdk/README.md)（待创建）
- [部署指南](../docs/deployment/)

## License

同主项目 License
