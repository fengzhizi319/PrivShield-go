# PrivShield 共享基础包 (Shared PKG) — 生产运维与加固手册

> **文档定位**：`pkg` 基础包及底层存储引擎、密码信封、mTLS 证书体系与中间件网关的生产部署、性能调优、监控告警与故障排查（Runbook）运维指南。

---

## 目录

- [一、全局运维配置参数表](#一全局运维配置参数表)
- [二、存储引擎运维与性能调优](#二存储引擎运维与性能调优)
  - [2.1 SQLite WAL 生产模式配置](#21-sqlite-wal-生产模式配置)
  - [2.2 PostgreSQL Phase B 分布式集群与连接池调优](#22-postgresql-phase-b-分布式集群与连接池调优)
  - [2.3 数据库自动化迁移工具 (`store/cmd/migrate`)](#23-数据库自动化迁移工具-storecmdmigrate)
- [三、商用密码与 mTLS 证书安全运维](#三商用密码与-mtls-证书安全运维)
  - [3.1 国密 SM4 信封主密钥管理规范](#31-国密-sm4-信封主密钥管理规范)
  - [3.2 mTLS 客户端证书与 CN 白名单热加载](#32-mtls-客户端证书与-cn-白名单热加载)
- [四、全栈监控大盘与 Prometheus 告警规则](#四全栈监控大盘与-prometheus-告警规则)
- [五、核心故障排查手册 (Runbook)](#五核心故障排查手册-runbook)

---

## 一、全局运维配置参数表

所有采用 `pkg/config` 加载的微服务均遵循以下标准化环境变量：

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `LOG_LEVEL` | string | `info` | `info` | 日志级别：`debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | string | `text` | `json` | 日志格式：`json`（对接 ELK/Loki）或 `text` |
| `DB_PATH` | string | `""` | `/var/lib/privshield/data.db` | SQLite 数据库落盘路径（为空则使用内存模式） |
| `PG_DSN` | string | `""` | `postgres://user:pwd@pg-ha:5432/privshield` | PostgreSQL Phase B 连接串（配置后优先启用） |
| `PG_MAX_CONNS` | int | 自动计算 | $N_{cpu} \times 4$ (最大 64) | PostgreSQL 连接池最大并发连接数 |
| `PG_MIN_CONNS` | int | 自动计算 | $\max(2, N_{cpu})$ | PostgreSQL 连接池最小保活空闲连接数 |
| `ENCRYPTION_KEY` | string | `""` | 32位高强度随机字符串 | 国密 SM4-GCM 快照信封加密主密钥 |
| `RATE_LIMIT_RPS` | int | `100` | `200 ~ 500` | 客户端 IP 令牌桶每秒补充速率 |
| `RATE_LIMIT_BURST` | int | `200` | `400 ~ 1000` | 客户端 IP 令牌桶突发最大容量 |
| `MAX_CONCURRENT` | int | `1000` | `1000 ~ 2000` | 全局最大处理中并发请求数，超载返回 503 |
| `MAX_BODY_SIZE` | int64 | `33554432` | `33554432` (32MB) | 单次请求体最大允许字节数 |
| `TLS_ENABLED` | bool | `false` | `true` | 是否启用 gRPC mTLS 双向认证 |
| `TLS_CERT_FILE` | string | `""` | `/etc/privshield/certs/server.crt` | 服务端证书文件路径 |
| `TLS_KEY_FILE` | string | `""` | `/etc/privshield/certs/server.key` | 服务端私钥文件路径 |
| `TLS_CA_FILE` | string | `""` | `/etc/privshield/certs/ca.crt` | 客户端认证 CA 证书路径 |
| `MTLS_WHITELIST_FILE`| string | `""` | `/etc/privshield/whitelist.yaml` | CN 白名单 YAML 配置文件路径（支持热重载） |

---

## 二、存储引擎运维与性能调优

### 2.1 SQLite WAL 生产模式配置

当部署为单节点轻量网关时，系统采用嵌入式 SQLite 作为存储。

#### 关键 PRAGMA 参数与机制：
1. **WAL 模式 (`PRAGMA journal_mode=WAL`)**：
   * 写入追加到 `-wal` 文件，读操作直接读取主库与 WAL 快照，实现读写互不阻塞；
2. **锁重试超时 (`PRAGMA busy_timeout=5000`)**：
   * 遇到瞬间写锁冲突时，自动在内核层进行最长 5,000ms 的指数重试，消除 `database is locked` 偶发异常；
3. **安全同步级别 (`PRAGMA synchronous=NORMAL`)**：
   * 相比 `FULL` 提升 10 倍以上写入性能，且在现代文件系统与 WAL 机制下仍保证掉电不损坏；
4. **外键约束 (`PRAGMA foreign_keys=ON`)**：
   * 级联保障快照表与审计日志表的数据完整性。

> [!TIP]
> **目录与权限建议**：确保 `DB_PATH` 所在目录具有可写权限，且同目录下有足够的磁盘空间用于生成 `-wal` 和 `-shm` 共享内存文件。

### 2.2 PostgreSQL Phase B 分布式集群与连接池调优

面向政务云多副本高并发部署场景，配置 `PG_DSN` 即可无缝切换至 PostgreSQL 存储集群。

#### 2.2.1 自适应连接池计算公式 (`pkg/store/postgres/postgres.go`)
系统根据宿主机/容器的可用 CPU 核心数自动调整连接池大小：
$$\text{MaxConns} = \min\left(64, \max\left(10, N_{cpu} \times 4\right)\right)$$
$$\text{MinConns} = \min\left(\text{MaxConns}, \max\left(2, N_{cpu}\right)\right)$$

* 连接最大空闲时间：`30m`；
* 连接最长生命周期：`1h`；
* 健康检查探测周期：`1m`。

#### 2.2.2 审计日志原生分区管理
PostgreSQL 审计日志表 `audit_logs` 采用按月范围分区（Range Partitioning）：
```sql
CREATE TABLE IF NOT EXISTS audit_logs (
    id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64),
    timestamp TIMESTAMPTZ NOT NULL,
    operation VARCHAR(32) NOT NULL,
    ...
    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

-- 月度分区示例
CREATE TABLE IF NOT EXISTS audit_logs_y2026m08 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');
```

### 2.3 数据库自动化迁移工具 (`store/cmd/migrate`)

`pkg/store/cmd/migrate` 提供了独立且幂等的数据库 Schema 升级 CLI 工具：

```bash
# 查看帮助
go run pkg/store/cmd/migrate/main.go -help

# 对 SQLite 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver sqlite -dsn /var/lib/privshield/data.db

# 对 PostgreSQL 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver postgres -dsn "postgres://user:pwd@127.0.0.1:5432/privshield?sslmode=disable"
```

---

## 三、商用密码与 mTLS 证书安全运维

### 3.1 国密 SM4 信封主密钥管理规范

快照存证表中的 `input_sample` 与 `output_sample` 在落盘前自动通过 `ENCRYPTION_KEY` 进行 SM4-GCM 认证加密。

1. **密钥生成**：生产环境必须通过密码学安全随机数生成 256 位（32 字符）以上主密钥：
   ```bash
   openssl rand -hex 16
   ```
2. **密钥注入**：严禁将密钥明文写入配置文件或 Git 仓库，必须通过政务云 KMS、K8s Secret 或专属环境变量 `ENCRYPTION_KEY` 注入。
3. **密钥轮转策略**：
   * 旧数据带 `enc:v1:...` 头部；
   * 轮转密钥时，通过后台离线脚本读取旧数据、用旧密钥解密、并用新密钥重新加密存盘。

### 3.2 mTLS 客户端证书与 CN 白名单热加载

gRPC 跨域接入采用 mTLS 双向认证，通过 CN 白名单文件精细控制访问权限：

#### 白名单配置文件格式 (`whitelist.yaml`)：
```yaml
version: "1.0"
default_scopes: [] # fail-closed: 未知客户端默认拒绝
entries:
  - cn: "service-hub-prod-01"
    scopes: ["*"]
    enabled: true
    description: "调度中枢生产主节点"

  - cn: "audit-collector-node"
    scopes: ["audit:write", "audit:verify"]
    enabled: true
    description: "审计存证同步节点"
```

* **热加载机制**：`WhitelistManager` 在每次请求时通过文件 `mtime` 轮询检查。修改 `whitelist.yaml` 后，**无需重启服务**，秒级生效。

---

## 四、全栈监控大盘与 Prometheus 告警规则

### 4.1 核心监控指标清单

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `http_requests_total` | Counter | `module`, `method`, `path`, `status` | HTTP 请求总量（关注 5xx 占比 > 1%） |
| `http_request_duration_seconds` | Histogram | `module`, `method`, `path` | 接口响应延迟（P99 应 < 100ms） |
| `http_requests_in_flight` | Gauge | `module` | 实时并发处理中的连接数 |
| `audit_flusher_flushed_total` | Counter | `module` | 微批刷盘成功写入总条数 |
| `audit_flusher_dropped_total` | Counter | `module` | 缓冲队列满溢丢弃/降级条数（正常应为 0） |
| `audit_flusher_failed_total` | Counter | `module` | 微批底层写入失败总次数（正常应为 0） |
| `agent_circuit_breaker_state` | Gauge | `target` | 上游 Agent 熔断器状态 (0=Closed, 1=Open, 2=HalfOpen) |

### 4.2 推荐 Prometheus 告警规则 (`alerts.yaml`)

```yaml
groups:
  - name: privshield_pkg_alerts
    rules:
      - alert: PrivShieldHigh5xxErrorRate
        expr: sum(rate(http_requests_total{status=~"5.."}[2m])) / sum(rate(http_requests_total[2m])) * 100 > 2
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "PrivShield 接口 5xx 错误率超过 2%"
          description: "模块 {{ $labels.module }} 在过去 2 分钟内 5xx 错误率达到 {{ $value }}%"

      - alert: PrivShieldAuditFlusherDroppedLogs
        expr: increase(audit_flusher_dropped_total[5m]) > 0
        for: 0m
        labels:
          severity: warning
        annotations:
          summary: "审计微批缓冲队列发生满溢降级"
          description: "模块 {{ $labels.module }} 在过去 5 分钟内发生了 {{ $value }} 条存证满溢，请检查存储写入延迟或调大 BUFFER_SIZE。"

      - alert: PrivShieldAgentCircuitBreakerOpen
        expr: agent_circuit_breaker_state == 1
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "上游隐私计算 Agent 熔断器已触发"
          description: "目标 {{ $labels.target }} 连续请求失败超过阈值，已进入熔断阻断状态，请排查 Python Agent 引擎状态。"
```

---

## 五、核心故障排查手册 (Runbook)

### 5.1 存证哈希链验真失败 (`POST /api/audit/chain/verify` 返回 `valid: false`)

* **现象**：调用核验接口返回 `broken_at_id: "log-xxx"`, `expected_hash != actual_hash`。
* **排查流程**：
  1. 检查 `broken_at_id` 记录的 `timestamp` 是否与前序记录颠倒（时钟跳变）；
  2. 检查数据库是否存在手动 `UPDATE` 或 `DELETE` 审计日志的操作；
  3. 检查是否有未经 `BufferedAuditStore` 的直接并发 SQL 插入绕过了单 Worker 哈希绑定；
  4. 检查是否存在时区配置未归一（已全面采用 `timestamp.UTC().Format(time.RFC3339Nano)` 标准格式）。

### 5.2 SQLite 锁等待超时 (`database is locked`)

* **现象**：高并发写入时日志中出现 `database is locked`。
* **排查流程**：
  1. 确认是否已装配 `flusher.BufferedAuditStore`（未装配时单条落盘易锁库）；
  2. 确认 SQLite 连接池最大打开数是否合理（SQLite 写入为单进程排他锁，`MaxOpenConns` 不宜超过 4）；
  3. 确认磁盘 I/O 状态（`iostat -x 1`），若磁盘队列过高，考虑将数据目录挂载至高速 NVMe SSD；
  4. 业务 QPS 持续超过 1,000 时，配置 `PG_DSN` 平滑迁移至 PostgreSQL Phase B。

### 5.3 敏感样本解密失败 (`failed to decrypt sample`)

* **现象**：调用快照查询接口返回密文或解密错误。
* **排查流程**：
  1. 检查当前实例的 `ENCRYPTION_KEY` 环境变量是否与数据写入时的密钥一致；
  2. 检查密文字符串是否以 `enc:v1:` 开头，Base64 字符串是否在传输中被截断或转义损坏；
  3. 检查数据库字符集是否为 UTF-8。
