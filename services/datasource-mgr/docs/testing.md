# 模拟数据源服务 (Mock Datasource Manager) — 测试规范

> 本文档详细说明 **数联天下 · 数盾 (`PrivShield`)** 模拟数据源模块（`services/datasource-mgr`）的测试架构、用例覆盖与执行方式。

---

## 1. 测试全景与模块覆盖

| 测试包 | 测试文件 | 覆盖内容与核心断言 |
|---|---|---|
| `internal/grpcserver` | `server_test.go` | **5 个 gRPC 方法**（`Health` / `ListDataSources` / `GetDataSource` / `TestConnection` / `GetRecordByIDCard`）、入参校验防御（空 id、不存在 id）、`BuildServerCredentials` 凭证构造、**HTTPS REST mTLS 握手校验与公钥固定 (SPKI Pinning)**（`TestBuildServerTLSConfig_HTTPS_MTLS`） |
| `internal/grpcserver` | `auth_test.go` | **gRPC 方法级 Scope 权限映射**（`DatasourceMgrPermissionForGRPCMethod`：查询类方法 → `datasource:read`，`TestConnection` → `datasource:admin`，`Health` / 未知方法 → 免鉴权）与 **Identity Scope 校验**（通配符 `*` 全授权、受限 Scope 仅匹配授权、空 Scope 全拒绝） |
| `internal/handlers` | `handlers_test.go` | **HTTP REST Handler 层**（Health 探针、数据源资产目录列表、数据源详情与 404、连通性测试、表结构元数据自动探查、访问审计、种子数据重置、**按身份证号查询单条记录** `TestGetRecordByIDCard`）与 **CSV 加载安全**（路径遍历防御 `TestLoadCSVRecords_PathTraversal`、白名单文件校验、出厂样例严格模式存活、损坏行严格模式中断而不静默降级） |
| `internal/config` | `config_test.go` | 默认配置、自定义环境变量加载、**fail-closed 零信任门禁**（`TestFailClosedDefaults`：严格存储默认 true、环回形态免密放行；`TestFailClosedRejections`：非环回监听缺 API Key → `ErrAPIKeyRequired`、`RequireTLS` 未启用 TLS → `ErrTLSRequired`、gRPC TLS 缺 CN 白名单文件 → `ErrMTLSWhitelistRequired`） |
| `internal/config` | `scripts_test.go` | **运维与启动脚本集成测试**（全套 Shell 脚本执行权限与 Bash 语法检查、`gen-certs.sh` 证书链与 X.509 属性校验、`dev-run.sh` 守护进程启动与 HTTP 探活、`prod-run.sh` 生产进程拉起与 **HTTPS (mTLS) + gRPC (mTLS)** 双协议握手联调） |
| `internal/models` | `models_test.go` | `MockDataSource`、`MetadataResponse`（含嵌套 `TableMetadata` / `MetadataField`）的 JSON 序列化与反序列化往返一致性边界测试 |

---

## 2. 核心安全测试用例

### 2.1 HTTPS 双向认证与公钥固定测试 (`TestBuildServerTLSConfig_HTTPS_MTLS`)
1. **Case 1 (合法双向认证)**：客户端挂载 `ca.crt` 根证书与匹配固定公钥 `client.pub` 的客户端私钥证书，访问 `https://127.0.0.1:<port>/api/health`，断言返回 **200 OK**；
2. **Case 2 (未挂载客户端证书)**：未提供证书直接请求 HTTPS 端口，底层 TLS 握手立即断开并报错 `tls: client didn't provide a certificate`，断言请求被严格阻断；
3. **Case 3 (公钥不匹配伪造证书)**：使用由合法 CA 签发但公钥非 `client.pub` 的证书访问，握手阶段被 `VerifyPeerCertificate` 阻断，实现零信任安全防御。

---

## 3. 运行测试命令

```bash
# 1. 运行 datasource-mgr 全部单元测试与脚本集成测试
go test -v ./services/datasource-mgr/...

# 2. 仅运行快速单元测试（跳过子进程拉起）
go test -v -short ./services/datasource-mgr/...

# 3. 运行全仓 Go 测试套件
make test-go
```
