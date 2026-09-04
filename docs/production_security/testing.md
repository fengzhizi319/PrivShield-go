# 生产安全加固测试文档

> **版本**：v16.0.0  
> **适用范围**：`PrivShield` 核心算力引擎（`engine`）、企业级中台微服务群（`service-hub` / `datasource-mgr` / `audit-log`）、控制台与双 BFF 体系（`bff-go` / `app-lz`）。  
> **定位**：定义 Python `engine/security/` 与 Go `pkg/tlsutil` / `pkg/middleware` 的测试策略、测试用例与执行方案。

---

## 目录

- [1. 概述与测试目标](#1-概述与测试目标)
- [2. Python 单元测试策略](#2-python-单元测试策略)
  - [2.1 动态自签名证书生成](#21-动态自签名证书生成)
  - [2.2 TLS 配置校验](#22-tls-配置校验)
  - [2.3 认证与鉴权测试 (API Key + mTLS CN 白名单)](#23-认证与鉴权测试-api-key--mtls-cn-白名单)
  - [2.4 速率限制与探针豁免](#24-速率限制与探针豁免)
  - [2.5 身份模型与 Scope 匹配](#25-身份模型与-scope-匹配)
- [3. 集成测试策略](#3-集成测试策略)
  - [3.1 REST TLS 集成测试](#31-rest-tls-集成测试)
  - [3.2 gRPC TLS/mTLS 集成测试](#32-grpc-tlsmtls-集成测试)
- [4. Go 共享安全库与微服务测试](#4-go-共享安全库与微服务测试)
- [5. 测试执行与验证命令](#5-测试执行与验证命令)
- [6. 验收检查清单](#6-验收检查清单)

---

## 1. 概述与测试目标

本文档定义 `engine/security/` 与 Go 共享安全栈的测试策略、测试范围与可执行示例。安全模块测试需覆盖 TLS/mTLS 握手、API Key 认证、接口级权限鉴权、速率限制、Slowloris/Payload DDoS 防护以及健康检查豁免。

---

## 2. Python 单元测试策略

### 2.1 动态自签名证书生成

复用 `tests/security_certs.py` 中的 `generate_test_certs`，避免在仓库中提交真实证书：

```python
from pathlib import Path
from tests.security_certs import generate_test_certs

def test_generate_certs(tmp_path: Path):
    certs = generate_test_certs(tmp_path)
    for name in ("ca_cert", "server_cert", "server_key", "client_cert", "client_key"):
        assert certs[name].exists()
```

### 2.2 TLS 配置校验

```python
import pytest
from engine.security.config import SecuritySettings


def test_tls_requires_cert_and_key():
    with pytest.raises(ValueError, match="PRIVACY_TLS_CERT_FILE and PRIVACY_TLS_KEY_FILE"):
        SecuritySettings(tls_enabled=True)


def test_mtls_requires_ca():
    with pytest.raises(ValueError, match="PRIVACY_TLS_CA_FILE is required"):
        SecuritySettings(
            tls_enabled=True,
            tls_cert_file="server.crt",
            tls_key_file="server.key",
            tls_client_auth="require",
        )
```

### 2.3 认证与鉴权测试 (API Key + mTLS CN 白名单)

```python
import pytest
from engine.security.auth import _authenticate_mtls
from engine.security.config import SecuritySettings


def test_mtls_default_disabled():
    """mTLS 认证默认关闭：仅凭 CA 校验通过的证书不能获得身份。"""
    settings = SecuritySettings()
    ctx = {"transport_security_type": [b"ssl"], "x509_common_name": [b"any-client"]}
    assert _authenticate_mtls(settings, ctx) is None


def test_mtls_cn_in_whitelist_grants_internal_identity():
    """CN 命中白名单的客户端获得内部身份，scope 为 ["*"]。"""
    settings = SecuritySettings(
        auth_internal_mtls_enabled=True,
        auth_mtls_allowed_cns=["internal-client"],
    )
    ctx = {"transport_security_type": [b"ssl"], "x509_common_name": [b"internal-client"]}
    ident = _authenticate_mtls(settings, ctx)
    assert ident is not None
    assert ident.service_type == "internal"
    assert ident.name == "internal-client"
    assert ident.scopes == ["*"]


def test_mtls_cn_not_in_whitelist_rejected():
    """CN 未命中白名单的证书被拒绝。"""
    settings = SecuritySettings(
        auth_internal_mtls_enabled=True,
        auth_mtls_allowed_cns=["allowed-svc"],
    )
    ctx = {"transport_security_type": [b"ssl"], "x509_common_name": [b"rogue-svc"]}
    assert _authenticate_mtls(settings, ctx) is None
```

### 2.4 速率限制与探针豁免

```python
import pytest
from fastapi.testclient import TestClient
from engine.main import app

client = TestClient(app)

@pytest.fixture
def tight_rate_limit(monkeypatch):
    monkeypatch.setenv("PRIVACY_RATE_LIMIT_ENABLED", "true")
    monkeypatch.setenv("PRIVACY_RATE_LIMIT_DEFAULT_RPS", "100")
    monkeypatch.setenv("PRIVACY_RATE_LIMIT_DEFAULT_BURST", "100")
    monkeypatch.setenv(
        "PRIVACY_RATE_LIMIT_PER_ENDPOINT_JSON",
        '{"/v1/privacy/mask":{"rps":1,"burst":1}}',
    )
    yield


def test_rate_limit_blocks_excess(tight_rate_limit):
    resp = client.post("/v1/privacy/mask", json={"field_name": "mobile", "value": "13812345678"})
    assert resp.status_code == 200

    resp = client.post("/v1/privacy/mask", json={"field_name": "mobile", "value": "13912345678"})
    assert resp.status_code == 429


def test_rate_limit_health_exempt(tight_rate_limit):
    for _ in range(5):
        assert client.get("/health").status_code == 200
```

### 2.5 身份模型与 Scope 匹配

```python
from engine.security.identity import Identity

def test_identity_wildcard():
    identity = Identity("internal", "service-hub", ["*"])
    assert identity.has_permission("privacy:dp")
    assert identity.has_permission("classification:read")

def test_identity_exact_scope():
    identity = Identity("external", "portal", ["privacy:mask"])
    assert identity.has_permission("privacy:mask")
    assert not identity.has_permission("privacy:dp")
```

---

## 3. 集成测试策略

### 3.1 REST TLS 集成测试

使用 `uvicorn.Server` 在后台线程启动应用，使用 `httpx` 访问 HTTPS：

```python
import contextlib
import os
import threading
import time
from pathlib import Path
from typing import Any

import httpx
import uvicorn

from engine.main import app
from engine.security.config import get_security_settings
from engine.security.tls import uvicorn_ssl_kwargs
from tests.security_certs import generate_test_certs


class RestServer:
    def __init__(self, port: int, ssl_kwargs: dict[str, Any], ca: Path):
        self._port = port
        self._server = uvicorn.Server(
            uvicorn.Config(app, host="127.0.0.1", port=port, log_level="warning", **ssl_kwargs)
        )
        self._thread = threading.Thread(target=self._server.run, daemon=True)
        self._ca = ca

    def start(self):
        self._thread.start()
        deadline = time.monotonic() + 10
        with httpx.Client(verify=str(self._ca)) as client:
            while time.monotonic() < deadline:
                try:
                    if client.get(f"https://127.0.0.1:{self._port}/health").status_code == 200:
                        return
                except Exception:
                    time.sleep(0.05)
        raise RuntimeError("REST server did not start")

    def stop(self):
        self._server.should_exit = True
        self._thread.join(timeout=5)
```

### 3.2 gRPC TLS/mTLS 集成测试

使用 `grpc.server` + `grpc_server_credentials` 启动 gRPCs 服务端，客户端使用 `grpc.ssl_channel_credentials`：

```python
def test_grpc_mtls_require_client_cert(tmp_path: Path):
    certs = generate_test_certs(tmp_path)
    port = 50052
    with grpc_tls_server(certs, port, client_auth="require"):
        # 无客户端证书，连接超时/失败
        ca = certs["ca_cert"].read_bytes()
        creds = grpc.ssl_channel_credentials(root_certificates=ca)
        with pytest.raises(grpc.FutureTimeoutError):
            with grpc.secure_channel(f"127.0.0.1:{port}", creds) as channel:
                grpc.channel_ready_future(channel).result(timeout=3)

        # 携带受信客户端证书，调用成功
        creds = grpc.ssl_channel_credentials(
            root_certificates=ca,
            private_key=certs["client_key"].read_bytes(),
            certificate_chain=certs["client_cert"].read_bytes(),
        )
        with grpc.secure_channel(f"127.0.0.1:{port}", creds) as channel:
            stub = privacy_pb2_grpc.PrivacyServiceStub(channel)
            resp = stub.Health(privacy_pb2.HealthRequest())
            assert resp.status == "ok"
```

---

## 4. Go 共享安全库与微服务测试

Go 安全模块覆盖测试：
1. `pkg/tlsutil/whitelist_test.go`：测试 CN 白名单加载、Scope 校验与文件 5 秒轮询热重载；
2. `pkg/middleware/middleware_test.go`：测试 `RateLimit` IP 令牌桶、`MaxBodySize` 413 切断、`MaxConcurrent` 503 熔断与 `Recovery` 异常脱敏；
3. `pkg/crypto/envelope_test.go`：测试 SM4-GCM 动态 Nonce 加解密与格式校验；
4. `services/audit-log` 9 要素连续哈希链与验真接口单测。

---

## 5. 测试执行与验证命令

```bash
# 1. 运行 Python 全部安全测试
PYTHONPATH=. pytest tests/security/ -v

# 2. 单独运行特定子模块安全测试
PYTHONPATH=. pytest tests/security/test_security_auth.py -v
PYTHONPATH=. pytest tests/security/test_security_tls.py -v
PYTHONPATH=. pytest tests/security/test_security_rate_limit.py -v
PYTHONPATH=. pytest tests/security/test_security_whitelist.py -v

# 3. 运行 Go 基础安全库与微服务中间件测试（带竞争检测）
go test -race -count=1 ./pkg/tlsutil/... ./pkg/middleware/... ./pkg/crypto/... ./services/audit-log/...

# 4. 执行端到端安全与 DDoS 综合集成测试
bash ./scripts/dev/integration-test-services.sh
```

---

## 6. 验收检查清单

- [x] 动态生成 CA/服务器/客户端证书并在测试中使用。
- [x] 仅服务端 TLS 下，受信 CA 可连接，不受信 CA 失败。
- [x] mTLS `require` 模式下，缺失客户端证书连接失败。
- [x] mTLS CN 白名单：命中白名单的 CN 获得内部身份，未命中的被拒绝。
- [x] mTLS 默认关闭（fail-closed）：未显式启用时即使证书合法也不授予身份。
- [x] 内部 API Key 可访问全部接口，外部 API Key 越权返回 403 / `PERMISSION_DENIED`。
- [x] 缺失/无效凭证返回 401 / `UNAUTHENTICATED`。
- [x] 超速调用返回 429 / `RESOURCE_EXHAUSTED`。
- [x] `/health` 与 `Health` 默认免认证、不限速。
- [x] 关闭安全开关后既有测试集无需修改即可通过。