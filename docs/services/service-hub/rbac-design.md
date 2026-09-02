# RBAC 角色权限与用户认证设计文档

> **数盾 PrivShield · service-hub 调度中枢** — 基于角色的访问控制 (Role-Based Access Control)

---

## 1. 概述

为 `service-hub` 调度中枢及其前端控制台 `console/app-lz` 引入 **双角色权限体系**，实现：

- **用户注册与登录**：JWT 令牌认证，无状态可扩展
- **常规用户 (user)**：数据通信、任务派发、拓扑查看等日常操作
- **管理员 (admin)**：全量权限 + 运维诊断、压测、E2E 测试等高权限操作

---

## 2. 角色定义

| 角色 | 标识 | 说明 | 典型使用场景 |
|------|------|------|-------------|
| 常规用户 | `user` | 通过通信发送数据、查看任务状态 | 业务系统调用数据 API、派发脱敏任务、查看审计日志 |
| 管理员 | `admin` | 全量权限 + 系统运维 | 性能压测、E2E 测试套件执行、指标监控、系统配置 |

---

## 3. 权限矩阵

### 3.1 service-hub REST API 端点权限

| 端点 | 方法 | user | admin | 说明 |
|------|------|:----:|:-----:|------|
| `/health`, `/readyz` | GET | ✅ | ✅ | 健康探针，公开无需认证 |
| `/api/auth/register` | POST | ✅ | ✅ | 用户注册，公开端点 |
| `/api/auth/login` | POST | ✅ | ✅ | 用户登录，公开端点 |
| `/api/auth/me` | GET | ✅ | ✅ | 获取当前用户信息 |
| `/api/lz/topology` | GET | ✅ | ✅ | 服务拓扑查看 |
| `/api/lz/data-api/definitions` | GET | ✅ | ✅ | 数据 API 定义列表 |
| `/api/lz/data-api/invoke` | POST | ✅ | ✅ | 调用数据 API |
| `/api/lz/tasks` | GET | ✅ | ✅ | 任务列表查看 |
| `/api/lz/tasks/:id` | GET | ✅ | ✅ | 任务详情查看 |
| `/api/lz/tasks/dispatch` | POST | ✅ | ✅ | 任务派发 |
| `/api/lz/tasks/leases` | GET | ✅ | ✅ | 租约信息查看 |
| `/api/lz/audit/logs` | GET | ✅ | ✅ | 审计日志查看 |
| `/api/lz/probe/all` | POST | ❌ | ✅ | 全集群拓扑探测（管理员） |
| `/api/lz/metrics` | GET | ❌ | ✅ | Prometheus 原始指标（管理员） |
| `/api/lz/metrics/parsed` | GET | ❌ | ✅ | 解析后性能指标（管理员） |
| `/api/lz/suites` | GET | ❌ | ✅ | 测试套件列表（管理员） |
| `/api/lz/suites/run` | POST | ❌ | ✅ | 执行测试套件（管理员） |

### 3.2 前端面板权限

| 面板 | user | admin | 说明 |
|------|:----:|:-----:|------|
| 四服务集群拓扑 | ✅ | ✅ | 服务健康状态总览 |
| 预设数据 API 测试 | ✅ | ✅ | 数据 API 全链路会话 |
| 任务生命周期与租约 | ✅ | ✅ | 任务管理与派发 |
| 性能与吞吐量压测 | ❌ | ✅ | 多协程并发压测 |
| 自动化测试套件 | ❌ | ✅ | E2E 测试执行 |
| 不可篡改审计验真 | ✅ | ✅ | 审计日志与 Merkle 验真 |
| 实时性能与分位数 | ❌ | ✅ | Prometheus 指标监控 |

---

## 4. 认证流程

```
┌──────────┐                    ┌──────────────┐                    ┌──────────────┐
│  前端 SPA │                    │  App-LZ BFF  │                    │ service-hub  │
└────┬─────┘                    └──────┬───────┘                    └──────┬───────┘
     │                                 │                                 │
     │  1. POST /api/auth/register     │                                 │
     │  {username, password, role}     │                                 │
     │────────────────────────────────▶│                                 │
     │  2. {token, user}               │                                 │
     │◀────────────────────────────────│                                 │
     │                                 │                                 │
     │  3. POST /api/auth/login        │                                 │
     │  {username, password}           │                                 │
     │────────────────────────────────▶│                                 │
     │  4. {token, user}               │                                 │
     │◀────────────────────────────────│                                 │
     │                                 │                                 │
     │  5. GET /api/lz/topology        │                                 │
     │  Authorization: Bearer <JWT>    │                                 │
     │────────────────────────────────▶│                                 │
     │                                 │  6. 校验 JWT + 提取 role        │
     │                                 │  7. 检查 role 是否有权限        │
     │                                 │  8. 转发到 service-hub          │
     │                                 │────────────────────────────────▶│
     │  9. Response                    │                                 │
     │◀────────────────────────────────│                                 │
     │                                 │                                 │
```

---

## 5. JWT 令牌设计

### 5.1 令牌结构

```json
{
  "header": {
    "alg": "HS256",
    "typ": "JWT"
  },
  "payload": {
    "sub": "admin_user",        // 用户名
    "role": "admin",            // 角色: user | admin
    "iat": 1725235200,          // 签发时间
    "exp": 1725321600           // 过期时间 (24h)
  }
}
```

### 5.2 安全考量

- **签名算法**: HMAC-SHA256（纯标准库实现，零外部依赖）
- **密钥管理**: 通过环境变量 `APP_LZ_JWT_SECRET` 配置，最少 32 字符
- **令牌有效期**: 默认 24 小时（可配置）
- **密码存储**: bcrypt 哈希（cost=12），不可逆

---

## 6. API 端点详细设计

### 6.1 用户注册

```
POST /api/auth/register
Content-Type: application/json

{
  "username": "admin_user",
  "password": "SecureP@ss123",
  "display_name": "系统管理员",
  "role": "admin"
}

Response 201:
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "username": "admin_user",
    "display_name": "系统管理员",
    "role": "admin",
    "created_at": "2026-09-01T12:00:00Z"
  }
}
```

### 6.2 用户登录

```
POST /api/auth/login
Content-Type: application/json

{
  "username": "admin_user",
  "password": "SecureP@ss123"
}

Response 200:
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "username": "admin_user",
    "display_name": "系统管理员",
    "role": "admin"
  }
}
```

### 6.3 获取当前用户

```
GET /api/auth/me
Authorization: Bearer eyJhbGciOiJIUzI1NiIs...

Response 200:
{
  "username": "admin_user",
  "display_name": "系统管理员",
  "role": "admin",
  "created_at": "2026-09-01T12:00:00Z"
}
```

---

## 7. 前端角色适配策略

### 7.1 登录流程

1. 用户访问控制台 → 检查 localStorage 中是否有有效 JWT
2. 无有效令牌 → 显示 LoginPage（登录/注册 Tab 切换）
3. 登录成功 → 存储 JWT 到 localStorage + AuthContext 状态
4. 后续请求自动附加 `Authorization: Bearer <JWT>` 头

### 7.2 侧边栏动态渲染

```
┌─────────────────────────────────┐
│  数盾 · 调度之眼    v1.8.0     │
│  ┌──────────────────────────┐  │
│  │ 🟢 All Ready             │  │
│  └──────────────────────────┘  │
│                                 │
│  [四服务集群拓扑]     ← user    │
│  [预设数据 API]       ← user    │
│  [任务生命周期]       ← user    │
│  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─  │
│  [性能压测]           ← admin   │
│  [自动化测试]         ← admin   │
│  [审计验真]           ← user    │
│  [实时指标]           ← admin   │
│                                 │
│  ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─  │
│  👤 admin_user (管理员)         │
│  [退出登录]                     │
│  [中文] [EN]                    │
└─────────────────────────────────┘
```

---

## 8. 环境变量配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `APP_LZ_AUTH_ENABLED` | `false` | 启用用户认证（关闭时所有请求放行） |
| `APP_LZ_JWT_SECRET` | `(auto-generated)` | JWT 签名密钥（最少 32 字符） |
| `APP_LZ_JWT_EXPIRY_HOURS` | `24` | JWT 令牌有效期（小时） |
| `APP_LZ_USER_DB_PATH` | `(empty)` | 用户数据持久化路径（空 = 内存模式） |

---

## 9. 向后兼容

- `APP_LZ_AUTH_ENABLED=false`（默认）时，系统行为与改造前完全一致
- 原有 `APP_LZ_API_KEY` 机制保留，作为服务间认证（BFF → service-hub）的出站凭证
- JWT 认证仅作用于前端用户（浏览器 → BFF），不影响服务间通信
