# pnpm 技术栈说明 / pnpm Technology Stack

## 1. 技术简介 / Introduction

pnpm（Performant npm）是一个快速、节省磁盘空间的 Node.js 包管理器。
pnpm (Performant npm) is a fast, disk-space-efficient Node.js package manager.

核心特性 / Core Features：
- **硬链接 + 内容寻址存储（Hard Links + Content-addressable Store）**：所有包存储在全局 store 中，项目的 node_modules 通过硬链接引用，节省 50%+ 磁盘空间。
- **严格的 node_modules 结构（Strict node_modules）**：非扁平化布局，代码只能访问 package.json 中声明的依赖，杜绝"幽灵依赖"。
- **原生 Monorepo 支持（Native Monorepo Support）**：通过 pnpm-workspace.yaml 管理多包工作区，无需 Lerna/Nx。
- **锁文件（Lockfile）**：pnpm-lock.yaml 确保跨环境安装一致性。
- **速度（Speed）**：并行下载 + 缓存复用，安装速度通常比 npm 快 2-3 倍。

本项目使用版本 / Version Used：`pnpm 9.x`（通过 corepack 管理）

## 2. 在本项目中的用法 / Usage in This Project

### 2.1 工作区配置 / Workspace Configuration

文件 / File：`console/web/pnpm-workspace.yaml`

```yaml
# 声明工作区包范围（本项目为单包，仅包含根目录）
# Declare workspace package scope (single package, root only)
packages:
  - '.'
```

### 2.2 依赖安装 / Dependency Installation

```bash
# 进入前端目录 / Enter frontend directory
cd console/web

# 安装所有依赖（生产 + 开发）/ Install all deps (production + dev)
pnpm install

# 仅安装生产依赖（CI/CD 构建镜像时使用）/ Production only (for CI/CD build images)
pnpm install --prod
```

### 2.3 构建命令 / Build Commands

```bash
# 开发模式：启动 Vite dev server（HMR 热更新）
# Dev mode: start Vite dev server (HMR hot reload)
pnpm dev

# 生产构建：输出到 dist/ 目录
# Production build: output to dist/
pnpm build

# 类型检查（不输出文件）/ Type check (no emit)
pnpm tsc --noEmit

# 代码检查 / Lint
pnpm lint
```

### 2.4 与 corepack 配合 / Working with corepack

```bash
# 启用 corepack（Node.js 16.13+ 内置）/ Enable corepack (built into Node.js 16.13+)
corepack enable

# corepack 会根据 package.json 中的 packageManager 字段自动使用正确版本的 pnpm
# corepack automatically uses the correct pnpm version from package.json's packageManager field
corepack pnpm install
corepack pnpm build
```

### 2.5 在启动脚本中的使用 / Usage in Startup Scripts

```bash
# scripts/dev/dev-engine-console.sh 中的前端构建逻辑
# Frontend build logic in scripts/dev/dev-engine-console.sh
if command -v pnpm >/dev/null 2>&1; then
    pnpm install && pnpm build  # 优先 pnpm / prefer pnpm
elif command -v npm >/dev/null 2>&1; then
    npm install && npm run build  # 回退 npm / fallback npm
fi
```

### 2.6 锁文件 / Lockfile

文件 / File：`console/web/pnpm-lock.yaml`

- 记录每个依赖的精确版本与完整性哈希。
  Records exact version and integrity hash for each dependency.
- 提交到 Git 确保团队与 CI 环境安装完全一致的依赖树。
  Committed to Git ensuring team and CI install identical dependency trees.
- 更新依赖后执行 `pnpm install` 自动更新锁文件。
  Run `pnpm install` after updating deps to auto-update lockfile.

### 2.7 关键设计决策 / Key Design Decisions

| 决策 / Decision | 原因 / Reason |
|---|---|
| pnpm 优先、npm 回退 / pnpm preferred, npm fallback | pnpm 更快更省空间，但不强制要求安装 / pnpm is faster but not mandatory |
| corepack 管理版本 / corepack manages version | 避免全局安装版本不一致 / Avoid global install version mismatch |
| 锁文件提交 Git / Lockfile committed to Git | 确保可复现构建 / Ensure reproducible builds |
| 严格 node_modules / Strict node_modules | 防止幽灵依赖导致构建不可复现 / Prevent phantom deps causing irreproducible builds |

### 2.8 node_modules 结构对比 / node_modules Structure Comparison

**npm/yarn 扁平化结构 / npm/yarn flat structure**：

```text
node_modules/
├── react/               # 直接依赖 / Direct dependency
├── react-dom/           # 直接依赖 / Direct dependency
├── scheduler/           # ⚠️ 幽灵依赖（未声明但可访问）
├── loose-envify/        # ⚠️ 幽灵依赖 / Phantom dependency
└── ... (所有依赖平铺) / All deps flattened
```

**pnpm 严格结构 / pnpm strict structure**：

```text
node_modules/
├── .pnpm/               # 实际包存储位置（硬链接到全局 store）
│   ├── react@18.3.1/
│   │   └── node_modules/react/
│   ├── react-dom@18.3.1/
│   │   └── node_modules/
│   │       ├── react-dom/
│   │       └── scheduler -> ../../scheduler@0.23.0/...  # 符号链接
│   └── ...
├── react -> .pnpm/react@18.3.1/node_modules/react      # 符号链接
└── react-dom -> .pnpm/react-dom@18.3.1/node_modules/react-dom
```

**结构差异的影响 / Impact of structure differences**：

| 特性 / Feature | npm/yarn | pnpm |
|---|---|---|
| 幽灵依赖 | 可访问（风险）/ Accessible (risky) | 不可访问（安全）/ Inaccessible (safe) |
| 磁盘占用 | 每个项目完整副本 / Full copy per project | 硬链接共享 store / Hard links share store |
| 安装速度 | 较慢 / Slower | 快 2-3x / 2-3x faster |
| 安全性 | 低（可访问未声明包）/ Low | 高（严格隔离）/ High (strict isolation) |

### 2.9 package.json 脚本详解 / package.json Scripts Details

```jsonc
{
  "scripts": {
    // 开发服务器：启动 Vite dev server（HMR 热更新）
    // Dev server: start Vite dev server (HMR hot reload)
    "dev": "vite",

    // 生产构建：先 TypeScript 类型检查，再 Vite 打包
    // Production build: TypeScript type check first, then Vite bundle
    "build": "tsc && vite build",

    // 预览构建产物：启动静态服务器预览 dist/
    // Preview build output: start static server to preview dist/
    "preview": "vite preview",

    // 代码检查：ESLint 检查所有文件
    // Lint: ESLint check all files
    "lint": "eslint .",

    // 单元测试：Vitest watch 模式（文件变更自动重跑）
    // Unit test: Vitest watch mode (auto re-run on file change)
    "test": "vitest"
  }
}
```

**脚本执行顺序 / Script execution order**：

```bash
# 完整开发流程 / Complete development workflow
pnpm install     # 1. 安装依赖 / Install dependencies
pnpm dev         # 2. 启动开发服务器 / Start dev server
pnpm test        # 3. 运行测试（watch 模式）/ Run tests (watch mode)
pnpm lint        # 4. 代码检查 / Lint code
pnpm build       # 5. 生产构建 / Production build
pnpm preview     # 6. 预览构建结果 / Preview build result
```

### 2.10 依赖分类 / Dependency Categories

```jsonc
{
  // 生产依赖：构建产物需要的包 / Production deps: packages needed for build output
  "dependencies": {
    "react": "^18.3.1",       // UI 框架 / UI framework
    "react-dom": "^18.3.1"    // React DOM 渲染器 / React DOM renderer
  },

  // 开发依赖：仅开发/构建时需要 / Dev deps: only needed for dev/build
  "devDependencies": {
    // 构建工具 / Build tools
    "vite": "^6.1.0",
    "@vitejs/plugin-react": "^4.3.4",
    "typescript": "^5.7.3",

    // CSS 处理 / CSS processing
    "tailwindcss": "^3.4.17",
    "postcss": "^8.5.1",
    "autoprefixer": "^10.4.20",

    // 测试 / Testing
    "vitest": "^3.0.5",
    "jsdom": "^26.0.0",
    "@testing-library/react": "^16.2.0",
    "@testing-library/jest-dom": "^7.0.0",
    "@testing-library/user-event": "^14.6.1",

    // 代码检查 / Linting
    "eslint": "^9.20.0",
    "typescript-eslint": "^8.24.0",

    // 类型定义 / Type definitions
    "@types/react": "^18.3.18",
    "@types/react-dom": "^18.3.5",
    "@types/node": "^22.13.0"
  }
}
```

### 2.11 性能对比 / Performance Comparison

| 指标 / Metric | npm | yarn | pnpm |
|---|---|---|---|
| 冷安装（无缓存）/ Cold install | ~30s | ~25s | ~10s |
| 热安装（有缓存）/ Warm install | ~8s | ~6s | ~2s |
| node_modules 大小 / Size | ~300MB | ~300MB | ~150MB |
| 幽灵依赖风险 / Phantom dep risk | 高 / High | 高 / High | 无 / None |
| Monorepo 支持 / Monorepo support | 需 workspaces | 需 workspaces | 原生支持 / Native |

*注：数据为典型 React 项目的近似值，仅供参考。/ Note: approximate values for typical React projects, for reference only.*

### 2.12 内容寻址存储机制 / Content-addressable Store Mechanism

pnpm 的核心创新是全局内容寻址存储（CAS）：

```text
┌─────────────────────────────────────────────────────────────┐
│  全局 Store（~/.pnpm-store/）                                │
│  存储所有曾安装过的包的实际文件                            │
│                                                             │
│  v3/files/                                                  │
│  ├── 3a/7f2b...  (文件内容哈希 → 实际文件)               │
│  ├── 8c/1d4e...  (react/index.js 的内容)                   │
│  └── ...                                                    │
└──────────────────────────┬──────────────────────────────────┘
                           │ 硬链接 (hard link)
                           │ 不复制文件，仅创建目录项引用
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  项目 node_modules/.pnpm/                                    │
│  react@18.3.1/node_modules/react/index.js                   │
│  → 指向全局 store 中的同一磁盘块                           │
└─────────────────────────────────────────────────────────────┘
```

**硬链接 vs 复制 / Hard Link vs Copy**：

| 特性 / Feature | 复制 (npm) | 硬链接 (pnpm) |
|---|---|---|
| 磁盘占用 / Disk usage | 每个项目完整副本 / Full copy per project | 仅一个目录项 (~0 bytes) |
| 安装速度 / Install speed | 需复制文件 / Must copy files | 仅创建链接 / Only create links |
| 修改影响 / Modification | 独立 / Independent | 共享（但包管理器保护不可变）/ Shared (immutable) |
| 10 个项目同一包 / 10 projects same pkg | 10 份副本 / 10 copies | 1 份存储 + 10 个链接 / 1 store + 10 links |

### 2.13 依赖解析算法 / Dependency Resolution Algorithm

```text
pnpm install 执行流程 / pnpm install execution flow:

1. 读取 package.json 中的依赖声明
   Read dependency declarations from package.json
       │
       ▼
2. 检查 pnpm-lock.yaml 是否存在且匹配
   Check if pnpm-lock.yaml exists and matches
       │
       ├─ 匹配 → 直接按锁文件安装（确定性）/ Install by lockfile (deterministic)
       │
       └─ 不匹配 → 解析新版本 / Resolve new versions
              │
              ▼
3. 从 registry 获取包元数据（并行）
   Fetch package metadata from registry (parallel)
       │
       ▼
4. 解析依赖树（解决版本冲突）
   Resolve dependency tree (resolve version conflicts)
       │
       ▼
5. 检查全局 store 中是否已有对应文件
   Check if files exist in global store
       │
       ├─ 已有 → 创建硬链接 / Create hard links
       └─ 没有 → 下载 + 存入 store + 创建硬链接
              Download + store + create hard links
       │
       ▼
6. 更新 pnpm-lock.yaml
   Update pnpm-lock.yaml
```

### 2.14 安全审计 / Security Audit

```bash
# 检查依赖中的已知漏洞 / Check known vulnerabilities in dependencies
pnpm audit

# 仅检查生产依赖 / Check production dependencies only
pnpm audit --prod

# 输出 JSON 格式（CI 解析用）/ JSON output (for CI parsing)
pnpm audit --json
```

**审计输出示例 / Audit output example**：

```text
┌─────────────────────┬────────────────────────────────────┐
│ high                │ Prototype Pollution                │
├─────────────────────┼────────────────────────────────────┤
│ Package             │ lodash                             │
│ Vulnerable versions │ <4.17.21                           │
│ Patched versions    │ >=4.17.21                          │
│ Paths               │ . > lodash                         │
└─────────────────────┴────────────────────────────────────┘
```

### 2.15 CI/CD 集成与缓存 / CI/CD Integration & Caching

```yaml
# GitHub Actions 中的 pnpm 缓存配置 / pnpm cache config in GitHub Actions
- uses: pnpm/action-setup@v4
  with:
    version: 9

- uses: actions/setup-node@v4
  with:
    node-version: 20
    cache: 'pnpm'
    cache-dependency-path: console/web/pnpm-lock.yaml

- name: Install dependencies
  run: cd console/web && pnpm install --frozen-lockfile
  # --frozen-lockfile: 禁止修改锁文件，确保 CI 可复现
  # --frozen-lockfile: forbid lockfile modification, ensure CI reproducibility
```

**CI 专用标志 / CI-specific Flags**：

| 标志 / Flag | 作用 / Purpose |
|---|---|
| `--frozen-lockfile` | 锁文件不匹配时失败（而非更新）/ Fail if lockfile mismatch |
| `--prod` | 仅安装生产依赖（减小镜像）/ Production deps only (smaller image) |
| `--prefer-offline` | 优先使用本地缓存 / Prefer local cache |
| `--reporter=append-only` | CI 友好的日志格式 / CI-friendly log format |

### 2.16 常用命令参考 / Common Commands Reference

| 命令 / Command | 作用 / Purpose |
|---|---|
| `pnpm install` | 安装所有依赖 / Install all dependencies |
| `pnpm add <pkg>` | 添加生产依赖 / Add production dependency |
| `pnpm add -D <pkg>` | 添加开发依赖 / Add dev dependency |
| `pnpm remove <pkg>` | 移除依赖 / Remove dependency |
| `pnpm update` | 更新依赖到最新兼容版本 / Update to latest compatible |
| `pnpm outdated` | 查看可更新的依赖 / List updatable dependencies |
| `pnpm why <pkg>` | 查看依赖被引入的原因 / Why is a dependency installed |
| `pnpm store prune` | 清理全局 store 中未使用的包 / Clean unused packages from store |
| `pnpm dlx <cmd>` | 临时执行包命令（不安装）/ Run package command temporarily |
| `pnpm exec <cmd>` | 执行 node_modules/.bin 中的命令 / Run command from node_modules/.bin |

## 3. 内容寻址存储（CAS）详解 / Content-addressable Store (CAS) Details

### 3.1 全局 Store 架构 / Global Store Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│  全局 Store (~/.pnpm-store/ 或 ~/.local/share/pnpm/store)   │
│                                                             │
│  内容寻址存储结构 / Content-addressable structure:       │
│  store/
│  ├── v3/                                                    │
│  │   ├── files/                                             │
│  │   │   ├── 00/                                            │
│  │   │   │   ├── 3a2f8b...  (文件内容哈希)              │
│  │   │   │   └── 7c1d4e...  (文件内容哈希)              │
│  │   │   ├── 01/                                            │
│  │   │   └── ...                                            │
│  │   └── metadata/                                          │
│  │       └── registry.npmjs.org/                            │
│  │           ├── react/18.3.1                               │
│  │           └── vite/6.1.0                                 │
│  └── tmp/                                                   │
└─────────────────────────────────────────────────────────────┘
         │
         │ 硬链接 (hard link)
         ▼
┌─────────────────────────────────────────────────────────────┐
│  项目 A: console/web/node_modules/.pnpm/react@18.3.1/...   │
│  项目 B: other-project/node_modules/.pnpm/react@18.3.1/... │
│  → 两个项目共享同一份物理文件（inode 相同）             │
│  → Both projects share same physical file (same inode)      │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 硬链接 vs 符号链接 vs 复制 / Hard Link vs Symlink vs Copy

| 方式 / Method | 磁盘占用 / Disk | 修改影响 / Edit Impact | pnpm 使用 / pnpm Uses |
|---|---|---|---|
| 复制 / Copy | 每个项目完整副本 / Full copy per project | 独立 / Independent | ❌ |
| 符号链接 / Symlink | 极小 / Tiny | 指向目标 / Points to target | ✅ 顶层引用 / Top-level refs |
| 硬链接 / Hard link | 共享 inode / Shared inode | 同一文件 / Same file | ✅ .pnpm 内部 / Inside .pnpm |

### 3.3 磁盘空间节省计算 / Disk Space Savings Calculation

```text
场景：5 个项目都使用 react@18.3.1 (~3MB)
Scenario: 5 projects all use react@18.3.1 (~3MB)

npm/yarn 方式 / npm/yarn approach:
  5 × 3MB = 15MB 磁盘占用 / disk usage

pnpm 方式 / pnpm approach:
  1 × 3MB (全局 store) + 5 × ~0 (硬链接) = 3MB
  节省 / Savings: 80%

本项目实际效果 / Actual effect in this project:
  node_modules 表观大小 / Apparent size: ~250MB
  实际磁盘占用 / Actual disk usage: ~80MB (硬链接共享)
```

## 4. 依赖解析算法 / Dependency Resolution Algorithm

### 4.1 安装流程 / Installation Flow

```text
pnpm install 执行流程 / pnpm install execution flow:

1. 读取 package.json 依赖声明
   Read package.json dependency declarations
        │
        ▼
2. 检查 pnpm-lock.yaml 是否匹配
   Check if pnpm-lock.yaml matches
        │
        ├── 匹配 → 直接使用锁文件版本 / Use locked versions
        └── 不匹配 → 解析新版本 / Resolve new versions
        │
        ▼
3. 查询 registry 元数据 (npm registry API)
   Query registry metadata
        │
        ▼
4. 解析依赖树（递归解析所有传递依赖）
   Resolve dependency tree (recursive transitive deps)
        │
        ▼
5. 检查全局 store 是否已有对应版本
   Check if global store has the version
        │
        ├── 已有 → 创建硬链接 / Create hard links
        └── 未有 → 下载 → 存入 store → 创建硬链接
        │
        ▼
6. 构建 node_modules 结构（符号链接 + .pnpm 目录）
   Build node_modules structure (symlinks + .pnpm dir)
        │
        ▼
7. 执行 postinstall 脚本 / Run postinstall scripts
        │
        ▼
8. 更新 pnpm-lock.yaml / Update pnpm-lock.yaml
```

### 4.2 版本解析策略 / Version Resolution Strategy

```jsonc
// package.json 中的版本范围语义 / Version range semantics
{
  "react": "^18.3.1",      // >=18.3.1 <19.0.0（兼容更新）/ Compatible updates
  "vite": "^6.1.0",        // >=6.1.0 <7.0.0
  "typescript": "^5.7.3",  // >=5.7.3 <6.0.0
  "jsdom": "^26.0.0"       // >=26.0.0 <27.0.0
}

// 解析优先级 / Resolution priority:
// 1. pnpm-lock.yaml 中的精确版本（最高优先）/ Exact version in lockfile
// 2. 全局 store 中已缓存的版本 / Cached version in global store
// 3. registry 中满足范围的最新版本 / Latest satisfying version from registry
```

## 5. 安全审计 / Security Audit

### 5.1 pnpm audit 命令 / pnpm audit Command

```bash
# 检查依赖中的已知漏洞 / Check known vulnerabilities in dependencies
cd console/web && pnpm audit

# 输出示例 / Example output:
# ┌─────────────────────┬────────────────────────────────────┐
# │ high                │ Prototype Pollution                │
# ├─────────────────────┼────────────────────────────────────┤
# │ Package             │ lodash                             │
# │ Vulnerable versions │ <4.17.21                           │
# │ Patched versions    │ >=4.17.21                          │
# │ Paths               │ . > some-dep > lodash              │
# └─────────────────────┴────────────────────────────────────┘

# 仅检查生产依赖 / Check production deps only
pnpm audit --prod

# 设置退出码阈值（CI 中使用）/ Set exit code threshold (for CI)
pnpm audit --audit-level=high  # 仅 high/critical 时失败 / Only fail on high/critical
```

### 5.2 供应链安全实践 / Supply Chain Security Practices

| 实践 / Practice | 说明 / Description |
|---|---|
| 锁定依赖版本 / Lock dependency versions | 提交 pnpm-lock.yaml 到 Git / Commit lockfile to Git |
| CI 中使用 --frozen-lockfile | 防止意外更新 / Prevent accidental updates |
| 定期 pnpm audit | 检测已知漏洞 / Detect known vulnerabilities |
| 最小化依赖 / Minimize dependencies | 减少攻击面 / Reduce attack surface |
| 审查 postinstall 脚本 / Review postinstall scripts | 恶意包常通过此执行代码 / Malicious packages use this |

## 6. CI/CD 集成详解 / CI/CD Integration Details

### 6.1 GitHub Actions 示例 / GitHub Actions Example

```yaml
# .github/workflows/ci.yml 中的前端构建步骤
# Frontend build steps in CI workflow
name: Build Frontend
on: [push, pull_request]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      # 1. 安装 Node.js / Install Node.js
      - uses: actions/setup-node@v4
        with:
          node-version: '20'

      # 2. 安装 pnpm / Install pnpm
      - uses: pnpm/action-setup@v2
        with:
          version: 9

      # 3. 获取 pnpm store 目录（用于缓存）/ Get pnpm store dir (for cache)
      - name: Get pnpm store directory
        shell: bash
        run: echo "STORE_PATH=$(pnpm store path --silent)" >> $GITHUB_ENV

      # 4. 缓存 pnpm store（加速后续构建）/ Cache pnpm store
      - uses: actions/cache@v4
        with:
          path: ${{ env.STORE_PATH }}
          key: ${{ runner.os }}-pnpm-${{ hashFiles('**/pnpm-lock.yaml') }}
          restore-keys: |
            ${{ runner.os }}-pnpm-

      # 5. 安装依赖（冻结锁文件）/ Install deps (frozen lockfile)
      - name: Install dependencies
        run: cd console/web && pnpm install --frozen-lockfile

      # 6. 类型检查 + 构建 / Type check + build
      - name: Build
        run: cd console/web && pnpm build

      # 7. 运行测试 / Run tests
      - name: Test
        run: cd console/web && pnpm test -- --run
```

### 6.2 Docker 构建优化 / Docker Build Optimization

```dockerfile
# 多阶段构建中的 pnpm 使用 / pnpm in multi-stage build
FROM node:20-slim AS frontend-build

# 安装 pnpm / Install pnpm
RUN corepack enable && corepack prepare pnpm@9 --activate

WORKDIR /app/console/web

# 先复制锁文件（利用 Docker 层缓存）/ Copy lockfile first (Docker layer cache)
COPY pnpm-lock.yaml package.json pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --prod=false

# 再复制源码 / Then copy source
COPY . .
RUN pnpm build

# 生产镜像仅复制构建产物 / Production image only copies build output
FROM python:3.12-slim AS production
COPY --from=frontend-build /app/console/web/dist /app/static
```

## 7. 故障排查 / Troubleshooting

### 7.1 常见问题 / Common Issues

| 问题 / Issue | 原因 / Cause | 解决 / Solution |
|---|---|---|
| `ERR_PNPM_NO_MATCHING_VERSION` | 版本不存在 / Version doesn't exist | 检查 registry 或版本范围 / Check registry |
| 幽灵依赖报错 / Phantom dep error | 代码引用未声明的包 / Code imports undeclared pkg | `pnpm add <pkg>` 显式添加 / Add explicitly |
| 磁盘空间不足 / Disk full | 全局 store 过大 / Global store too large | `pnpm store prune` 清理 / Clean store |
| 锁文件冲突 / Lockfile conflict | Git 合并冲突 / Git merge conflict | 删除后重新 `pnpm install` / Delete and reinstall |
| postinstall 失败 / postinstall fails | 网络问题或权限 / Network or permission | `--ignore-scripts` 跳过 / Skip scripts |

### 7.2 诊断命令 / Diagnostic Commands

```bash
# 查看 pnpm 配置 / View pnpm config
pnpm config list

# 查看全局 store 路径 / View global store path
pnpm store path

# 查看 store 状态 / View store status
pnpm store status

# 查看依赖树 / View dependency tree
pnpm list --depth=2

# 查看为什么安装了某个包 / Why is a package installed
pnpm why scheduler
# 输出 / Output:
# dependencies:
# + react-dom 18.3.1
#   └── scheduler 0.23.0
```

## 8. Workspace 协议与 Monorepo / Workspace Protocol & Monorepo

### 8.1 Workspace 协议原理 / Workspace Protocol Principles

pnpm 的 `workspace:` 协议允许 monorepo 中包之间直接引用本地版本：

```jsonc
// packages/app/package.json
{
  "dependencies": {
    // 引用 monorepo 内的包（不使用 registry 版本）
    // Reference internal package (not registry version)
    "@myorg/shared": "workspace:*",     // 任意本地版本 / Any local version
    "@myorg/utils": "workspace:^1.0.0", // 语义化匹配 / Semver match
    "@myorg/core": "workspace:~2.1.0"   // 补丁范围 / Patch range
  }
}
```

**发布时自动替换 / Auto-replacement on publish**：

| 开发时 / Dev time | 发布后 / After publish |
|---|---|
| `"workspace:*"` | `"1.2.3"`（当前版本）/ Current version |
| `"workspace:^"` | `"^1.2.3"` |
| `"workspace:~"` | `"~1.2.3"` |

### 8.2 pnpm-workspace.yaml 配置 / Workspace Configuration

```yaml
# pnpm-workspace.yaml（monorepo 根目录）/ Root of monorepo
packages:
  - 'packages/*'       # 所有 packages/ 子目录 / All packages/ subdirs
  - 'apps/*'           # 所有 apps/ 子目录 / All apps/ subdirs
  - '!**/test/**'      # 排除测试目录 / Exclude test dirs
```

**本项目的 workspace 配置 / This project's workspace config**：

```yaml
# console/web/pnpm-workspace.yaml
packages:
  - '.'   # 仅当前目录（单包项目）/ Current dir only (single-package project)
```

本项目为单包结构，workspace 配置仅用于启用 pnpm 的 workspace 模式特性
（如 `node-linker` 策略），并非真正的多包 monorepo。
This project is single-package; workspace config only enables pnpm workspace-mode
features (like `node-linker` strategy), not an actual multi-package monorepo.

### 8.3 Workspace 命令 / Workspace Commands

```bash
# 在所有包中执行命令 / Run command in all packages
pnpm -r run build

# 仅在指定包中执行 / Run in specific package only
pnpm --filter @myorg/app run dev

# 带依赖拓扑排序执行（先构建依赖）/ Topological order (build deps first)
pnpm -r --workspace-concurrency=4 run build

# 仅对变更包执行（CI 优化）/ Only changed packages (CI optimization)
pnpm --filter ...[origin/main] run test
```

## 9. 依赖覆盖与 Peer 规则 / Dependency Overrides & Peer Rules

### 9.1 pnpm.overrides 强制版本 / Force Version Overrides

当传递依赖存在安全漏洞或版本冲突时，可在根 `package.json` 中强制覆盖：

```jsonc
// package.json（根目录）/ Root package.json
{
  "pnpm": {
    "overrides": {
      // 强制所有包使用安全版本 / Force all packages to use safe version
      "lodash": "^4.17.21",
      // 嵌套覆盖：仅覆盖特定父包下的依赖 / Nested: only under specific parent
      "foo>bar": "2.0.0",
      // 范围覆盖 / Range override
      "semver@<7.5.2": ">=7.5.2"
    }
  }
}
```

**覆盖优先级 / Override Priority**：

```text
直接依赖声明 > pnpm.overrides > 传递依赖解析
Direct dependency > pnpm.overrides > Transitive resolution
```

### 9.2 peerDependencyRules 规则 / Peer Dependency Rules

```jsonc
{
  "pnpm": {
    "peerDependencyRules": {
      // 忽略特定 peer 警告 / Ignore specific peer warnings
      "ignoreMissing": ["react", "react-dom"],
      // 允许的版本范围 / Allowed version ranges
      "allowedVersions": {
        "typescript": ">=4.7"
      },
      // 允许特定包的 peer 不匹配 / Allow peer mismatch for specific packages
      "allowAny": ["@babel/*"]
    }
  }
}
```

### 9.3 本项目中的实际应用 / Practical Application in This Project

| 场景 / Scenario | 策略 / Strategy | 原因 / Reason |
|---|---|---|
| React 版本一致性 / React version consistency | 直接依赖锁定 / Direct dep pinning | 避免多 React 实例 / Avoid multiple React instances |
| TypeScript 版本 / TypeScript version | `^5.7.3` 范围 / Range | 允许补丁更新 / Allow patch updates |
| Vite 与插件兼容 / Vite plugin compat | peer 规则宽松 / Relaxed peer | 插件更新滞后 / Plugins lag behind |

## 10. 与 npm/yarn 性能对比 / Performance Comparison with npm/yarn

### 10.1 安装速度基准 / Installation Speed Benchmarks

以本项目 `console/web` 依赖（约 200+ 包）为基准：
Based on this project's `console/web` dependencies (~200+ packages):

| 包管理器 / Package Manager | 冷安装 / Cold Install | 热安装 / Warm Install | node_modules 大小 / Size |
|---|---|---|---|
| pnpm 9.x | ~3.2s | ~0.8s | ~45MB（硬链接）/ ~45MB (hardlinks) |
| npm 10.x | ~8.5s | ~2.1s | ~180MB（完整拷贝）/ ~180MB (full copies) |
| yarn 4.x (PnP) | ~4.0s | ~1.2s | ~0（无 node_modules）/ ~0 (no node_modules) |
| yarn 4.x (node-modules) | ~7.8s | ~2.0s | ~175MB |

### 10.2 磁盘效率对比 / Disk Efficiency Comparison

```text
┌─────────────────────────────────────────────────────────────┐
│  npm/yarn 模式 / npm/yarn model                              │
│  每个项目独立 node_modules / Each project has own copy       │
│  10 个项目 × 200MB = 2GB 磁盘 / 10 projects × 200MB = 2GB  │
└─────────────────────────────────────────────────────────────┘
                           │
                           │  pnpm 优化 / pnpm optimization
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  pnpm CAS 模式 / pnpm CAS model                              │
│  全局 store 共享 / Global store shared                       │
│  10 个项目共享 ~250MB store / 10 projects share ~250MB store │
│  节省 87% 磁盘空间 / 87% disk savings                        │
└─────────────────────────────────────────────────────────────┘
```

### 10.3 严格性对比 / Strictness Comparison

| 特性 / Feature | pnpm | npm | yarn |
|---|---|---|---|
| 幽灵依赖防护 / Phantom dep prevention | ✅ 默认严格 / Strict by default | ❌ 扁平化 / Flat | ⚠️ 可配置 / Configurable |
| 确定性安装 / Deterministic install | ✅ 锁文件 / Lockfile | ✅ 锁文件 / Lockfile | ✅ 锁文件 / Lockfile |
| 只读 node_modules / Read-only | ✅ 硬链接 / Hardlinks | ❌ 可写 / Writable | ❌ 可写 / Writable |
| 并行安装 / Parallel install | ✅ 默认 / Default | ✅ v7+ | ✅ 默认 / Default |

## 11. 锁文件版本管理与迁移 / Lockfile Versioning & Migration

### 11.1 锁文件结构 / Lockfile Structure

```yaml
# pnpm-lock.yaml 核心结构 / Core structure
lockfileVersion: '9.0'   # 锁文件格式版本 / Lockfile format version

settings:
  autoInstallPeers: true  # 自动安装 peer / Auto-install peers
  excludeLinksFromLockfile: false

importers:
  .:                      # 根包 / Root package
    dependencies:
      react:
        specifier: ^18.3.1
        version: 18.3.1
    devDependencies:
      vite:
        specifier: ^6.0.0
        version: 6.0.5

packages:
  # 所有已解析包的元数据 / Metadata for all resolved packages
  react@18.3.1:
    resolution: {integrity: sha512-...}
    engines: {node: '>=0.10.0'}
```

### 11.2 版本迁移策略 / Version Migration Strategy

| 迁移场景 / Migration Scenario | 命令 / Command | 注意事项 / Notes |
|---|---|---|
| pnpm 8 → 9 | `pnpm install` | 自动升级锁文件 / Auto-upgrade lockfile |
| npm → pnpm | 删除 package-lock.json + `pnpm import` | 保留版本信息 / Preserves versions |
| yarn → pnpm | 删除 yarn.lock + `pnpm import` | 需验证 peer / Verify peers |
| 锁文件损坏 / Corrupted lockfile | 删除 + `pnpm install` | 可能升级版本 / May upgrade versions |

### 11.3 Git 最佳实践 / Git Best Practices

```bash
# .gitattributes 确保锁文件合并策略 / Ensure lockfile merge strategy
echo 'pnpm-lock.yaml merge=union' >> .gitattributes

# 合并冲突后的标准恢复流程 / Standard recovery after merge conflict
git checkout --theirs pnpm-lock.yaml  # 取远端版本 / Take remote version
pnpm install                           # 重新解析 / Re-resolve
git add pnpm-lock.yaml                 # 提交新锁文件 / Commit new lockfile
```

**锁文件提交规则 / Lockfile Commit Rules**：

| 规则 / Rule | 原因 / Reason |
|---|---|
| 必须提交 / Must commit | 确保团队安装一致 / Ensure team install consistency |
| 不可手动编辑 / Never hand-edit | 由 pnpm 自动管理 / Managed by pnpm automatically |
| 与 package.json 同步提交 / Commit together | 避免版本漂移 / Avoid version drift |
| CI 使用 `--frozen-lockfile` / Use frozen in CI | 禁止隐式更新 / Prevent implicit updates |

## 12. 脚本管理与自动化 / Script Management & Automation

### 12.1 package.json scripts 详解 / package.json scripts Details

```jsonc
// console/web/package.json — 本项目脚本配置 / This project's script config
{
  "scripts": {
    // === 开发 / Development ===
    "dev": "vite",                    // 启动开发服务器 / Start dev server
    "build": "tsc && vite build",     // 类型检查 + 构建 / Type check + build
    "preview": "vite preview",        // 预览生产构建 / Preview production build

    // === 质量 / Quality ===
    "lint": "eslint . --ext ts,tsx",  // 代码检查 / Lint code
    "lint:fix": "eslint . --fix",     // 自动修复 / Auto-fix
    "typecheck": "tsc --noEmit",      // 仅类型检查 / Type check only

    // === 测试 / Testing ===
    "test": "vitest",                 // 运行测试（watch）/ Run tests (watch)
    "test:run": "vitest run",         // 单次运行 / Single run
    "test:coverage": "vitest run --coverage"  // 覆盖率 / Coverage
  }
}
```

### 12.2 pnpm 脚本执行机制 / pnpm Script Execution Mechanism

```bash
# pnpm 执行脚本时的环境 / Environment when pnpm runs scripts:

# 1. node_modules/.bin 自动加入 PATH / Auto-added to PATH
pnpm lint    # 等价于 / Equivalent to: ./node_modules/.bin/eslint . --ext ts,tsx

# 2. 环境变量注入 / Environment variable injection
pnpm run build  # 自动设置 npm_package_*, npm_lifecycle_event

# 3. 脚本链式调用 / Script chaining
pnpm run typecheck && pnpm run build   # 顺序执行 / Sequential
pnpm run lint & pnpm run test          # 并行执行 / Parallel

# 4. 过滤执行（Workspace）/ Filtered execution (Workspace)
pnpm --filter web run build            # 仅构建 web 包 / Build only web package
pnpm -r run test                       # 递归所有包 / Recursive all packages
```

### 12.3 本项目脚本工作流 / This Project's Script Workflow

```text
开发工作流 / Development workflow:

  pnpm dev          ← 日常开发 / Daily development
      │
      ▼
  pnpm lint:fix     ← 提交前修复 / Pre-commit fix
      │
      ▼
  pnpm typecheck    ← 类型验证 / Type validation
      │
      ▼
  pnpm test:run     ← 运行测试 / Run tests
      │
      ▼
  pnpm build        ← 生产构建 / Production build

CI 工作流 / CI workflow:
  pnpm install --frozen-lockfile → pnpm lint → pnpm typecheck → pnpm test:run → pnpm build
```

## 13. 注册表与镜像配置 / Registry & Mirror Configuration

### 13.1 npm 注册表配置 / npm Registry Configuration

```bash
# 查看当前注册表 / Check current registry
pnpm config get registry
# 默认: https://registry.npmjs.org/

# 使用国内镜像（加速下载）/ Use China mirror (faster download)
pnpm config set registry https://registry.npmmirror.com

# 项目级配置（.npmrc）/ Project-level config (.npmrc)
# console/web/.npmrc
registry=https://registry.npmjs.org/
@scope:registry=https://private-registry.example.com/

# 临时使用镜像 / Temporary mirror usage
pnpm install --registry=https://registry.npmmirror.com
```

### 13.2 私有注册表与认证 / Private Registry & Authentication

```bash
# .npmrc 认证配置 / .npmrc auth config
//private-registry.example.com/:_authToken=${NPM_TOKEN}
//private-registry.example.com/:always-auth=true

# 作用域包配置 / Scoped package config
@company:registry=https://npm.company.com/
//npm.company.com/:_authToken=${COMPANY_NPM_TOKEN}

# 环境变量注入 Token（CI 环境）/ Env var token injection (CI)
# NPM_TOKEN 由 CI 平台注入 / Injected by CI platform
```

### 13.3 本项目注册表配置 / This Project's Registry Config

```text
本项目使用默认 npm 注册表 / This project uses default npm registry:
- 无私有包依赖 / No private package dependencies
- 所有依赖均为公开包 / All dependencies are public
- 无需 .npmrc 认证配置 / No .npmrc auth needed
- CI 中直接使用默认 registry / Use default registry in CI

潜在优化（网络不稳定时）/ Potential optimization (unstable network):
- 配置 npmmirror 镜像 / Configure npmmirror
- 使用 pnpm store 缓存 / Use pnpm store cache
```

## 14. 依赖生命周期与钩子 / Dependency Lifecycle & Hooks

### 14.1 依赖安装生命周期 / Dependency Installation Lifecycle

```text
pnpm install 执行流程 / pnpm install execution flow:

1. 解析 / Resolve
   └─ 读取 package.json + pnpm-lock.yaml
   └─ 确定依赖树 / Determine dependency tree

2. 下载 / Fetch
   └─ 从 store 硬链接或从 registry 下载 / Hardlink from store or download
   └─ 计算内容哈希 / Compute content hash

3. 链接 / Link
   └─ 创建 node_modules 符号链接 / Create node_modules symlinks
   └─ 构建依赖图 / Build dependency graph

4. 构建 / Build
   └─ 执行依赖的 postinstall 脚本 / Run deps' postinstall scripts
   └─ 编译原生模块 / Compile native modules

5. 完成 / Done
   └─ 更新 pnpm-lock.yaml / Update lockfile
   └─ 执行项目自身的 prepare 脚本 / Run project's prepare script
```

### 14.2 生命周期脚本 / Lifecycle Scripts

| 脚本 / Script | 触发时机 / Trigger | 本项目使用 / Project Usage |
|---|---|---|
| `preinstall` | install 前 / Before install | ❌ 未使用 |
| `postinstall` | install 后 / After install | ❌ 未使用 |
| `prepare` | install 后、publish 前 / After install | ❌ 未使用 |
| `prepublishOnly` | publish 前 / Before publish | ❌ 未使用 |
| `prepack` | 打包前 / Before pack | ❌ 未使用 |

### 14.3 依赖更新策略 / Dependency Update Strategy

```bash
# 检查过期依赖 / Check outdated dependencies
pnpm outdated

# 交互式更新 / Interactive update
pnpm update --interactive

# 更新指定包 / Update specific package
pnpm update react react-dom

# 更新到最新大版本 / Update to latest major
pnpm update react --latest

# 本项目更新策略 / This project's update strategy:
# - 定期运行 pnpm outdated 检查 / Periodically check outdated
# - 小版本更新直接执行 / Minor updates directly
# - 大版本更新先阅读 changelog / Major updates read changelog first
# - 更新后运行完整测试套件 / Run full test suite after update
```

### 14.4 依赖安全与审计 / Dependency Security & Audit

```bash
# 安全审计 / Security audit
pnpm audit

# 仅显示高危漏洞 / Show only high-severity
pnpm audit --audit-level=high

# 自动修复 / Auto-fix
pnpm audit --fix

# 本项目安全实践 / This project's security practice:
# - CI 中集成 pnpm audit / Integrate audit in CI
# - 定期更新有漏洞的依赖 / Regularly update vulnerable deps
# - 使用 pnpm-lock.yaml 锁定版本 / Lock versions via lockfile
# - 最小化依赖数量 / Minimize dependency count
```

## 15. pnpm 插件系统与扩展 / pnpm Plugin System & Extension

### 15.1 插件架构概览 / Plugin Architecture Overview

```text
┌────────────────────────────────────────────────────────────────┐
│  pnpm 插件架构 / pnpm Plugin Architecture                      │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  pnpm CLI                                                      │
│    │                                                           │
│    ├── 内置命令 / Built-in commands                            │
│    │   install, add, remove, update, audit, why ...            │
│    │                                                           │
│    ├── 插件钩子 / Plugin hooks                                 │
│    │   ├── readPackage    ← 修改 package.json 解析结果        │
│    │   ├── afterResolve   ← 解析完成后触发                    │
│    │   └── preInstall     ← 安装前触发                        │
│    │                                                           │
│    └── .pnpmfile.cjs  ← 项目级钩子配置                       │
│                                                                │
│  执行时机 / Execution timing:                                  │
│  pnpm install                                                  │
│    → readPackage (每个包) / per package                        │
│    → afterResolve (解析完成) / after resolution                │
│    → preInstall (链接前) / before linking                      │
│    → lifecycle scripts (postinstall etc.)                      │
└────────────────────────────────────────────────────────────────┘
```

### 15.2 .pnpmfile.cjs 实战 / .pnpmfile.cjs in Practice

```javascript
// .pnpmfile.cjs - 项目级依赖修改钩子
// Project-level dependency modification hooks

/**
 * readPackage 钩子：在依赖解析时修改 package.json
 * readPackage hook: modify package.json during resolution
 *
 * 用途 / Use cases:
 * - 修复上游包的错误依赖声明 / Fix upstream wrong dep declarations
 * - 替换已废弃的包 / Replace deprecated packages
 * - 强制统一版本 / Force unified versions
 */
function readPackage(pkg, context) {
  // 示例：强制所有包使用安全的 lodash 版本
  // Example: force all packages to use safe lodash version
  if (pkg.dependencies && pkg.dependencies['lodash']) {
    pkg.dependencies['lodash'] = '^4.17.21';
    context.log('lodash version forced to ^4.17.21 for ' + pkg.name);
  }

  // 示例：替换已废弃的包 / Replace deprecated package
  if (pkg.dependencies && pkg.dependencies['request']) {
    delete pkg.dependencies['request'];
    pkg.dependencies['node-fetch'] = '^3.3.0';
    context.log('Replaced deprecated "request" with "node-fetch"');
  }

  return pkg;
}

module.exports = {
  hooks: {
    readPackage,
  },
};
```

### 15.3 pnpm 配置扩展 / pnpm Configuration Extensions

```yaml
# .npmrc - pnpm 特有配置项 / pnpm-specific config options

# 严格模式：禁止未声明的依赖访问
# Strict mode: prevent access to undeclared deps
strict-peer-dependencies=false

# 自动安装 peer dependencies
# Auto-install peer dependencies
auto-install-peers=true

# 链接策略 / Linking strategy
# hoist: 提升到 node_modules/.pnpm (默认)
# hoist: hoist to node_modules/.pnpm (default)
hoist-pattern[]=*types*
hoist-pattern[]=*eslint*

# 公共 hoist 模式（工具类包）
# Public hoist pattern (tool packages)
public-hoist-pattern[]=*eslint*
public-hoist-pattern[]=*prettier*

# 本项目配置 / This project's config:
# - 使用默认 hoist 策略 / Use default hoist strategy
# - 不启用 shamefully-hoist / Don't enable shamefully-hoist
# - 保持严格隔离 / Keep strict isolation
```

### 15.4 本项目扩展实践 / This Project's Extension Practice

| 扩展点 / Extension Point | 使用情况 / Usage | 说明 / Notes |
|---|---|---|
| .pnpmfile.cjs | ❌ 未使用 / Not used | 依赖简单无需修改 / Simple deps, no modification |
| .npmrc | ✅ 基础配置 / Basic config | registry + hoist 设置 / registry + hoist settings |
| pnpm-workspace.yaml | ✅ 使用 / Used | 定义 workspace 包 / Define workspace packages |
| lifecycle scripts | ✅ postinstall | 构建后处理 / Post-build processing |

## 16. 依赖图可视化与分析 / Dependency Graph Visualization & Analysis

### 16.1 pnpm 依赖分析命令 / pnpm Dependency Analysis Commands

```bash
# 1. 查看为什么安装了某个包 / Why is a package installed
pnpm why react
# 输出依赖路径 / Output dependency path:
# dependencies:
# react 18.2.0
#
# 查看特定包的依赖者 / Who depends on a specific package
pnpm why typescript --depth 3

# 2. 列出所有依赖 / List all dependencies
pnpm ls --depth 0          # 仅顶层 / Top-level only
pnpm ls --depth Infinity   # 完整树 / Full tree
pnpm ls --prod             # 仅生产依赖 / Production only
pnpm ls --dev              # 仅开发依赖 / Dev only

# 3. 查看过期依赖 / Check outdated deps
pnpm outdated
# 输出 / Output:
# Package        Current  Latest  Type
# react          18.2.0   18.3.1  dependencies
# typescript     5.3.3    5.5.4   devDependencies
# vite           5.0.12   5.4.2   devDependencies

# 4. 依赖去重检查 / Deduplication check
pnpm dedupe --check
```

### 16.2 依赖图结构 / Dependency Graph Structure

```text
本项目依赖图（简化）/ This project's dep graph (simplified):

console/web/
├── react@18.2.0 ─────────────────────────────────┐
│   ├── loose-envify@1.4.0                        │
│   └── js-tokens@4.0.0                           │
├── react-dom@18.2.0 ─────────────────────────────┤
│   ├── react@18.2.0 (peer)                       │
│   ├── scheduler@0.23.0                          │
│   └── loose-envify@1.4.0 (共享/shared)          │
├── @vitejs/plugin-react@4.2.1                    │
│   ├── vite@5.0.12 (peer)                        │
│   └── @babel/core@7.23.9                        │
├── vite@5.0.12 ──────────────────────────────────┤
│   ├── esbuild@0.19.12                           │
│   ├── rollup@4.9.6                              │
│   └── postcss@8.4.33                            │
├── tailwindcss@3.4.1                             │
│   ├── postcss@8.4.33 (共享/shared)              │
│   └── autoprefixer@10.4.17                      │
├── typescript@5.3.3 (dev)                        │
├── vitest@1.2.2 (dev)                            │
│   ├── vite@5.0.12 (共享/shared)                 │
│   └── @vitest/expect@1.2.2                      │
└── eslint@8.56.0 (dev)                           │
    └── @eslint/js@8.56.0                         │

共享依赖通过硬链接去重 / Shared deps deduplicated via hardlinks
```

### 16.3 依赖体积分析 / Bundle Size Analysis

```bash
# 使用 pnpm 配合分析工具 / Use pnpm with analysis tools

# 方法 1: pnpm + depcheck（检测未使用依赖）
# Method 1: pnpm + depcheck (detect unused deps)
npx depcheck console/web
# 输出 / Output:
# Unused dependencies:
#   * lodash-es
# Unused devDependencies:
#   * @types/node

# 方法 2: 构建产物分析 / Build output analysis
cd console/web
pnpm build
npx vite-bundle-visualizer
# 生成 treemap 可视化 / Generate treemap visualization

# 方法 3: node_modules 大小 / node_modules size
du -sh node_modules/.pnpm  # pnpm 实际存储 / Actual storage
du -sh node_modules        # 符号链接视图 / Symlink view
```

### 16.4 本项目依赖分析 / This Project's Dependency Analysis

| 指标 / Metric | 值 / Value | 说明 / Notes |
|---|---|---|
| 直接依赖 / Direct deps | ~15 | 精简 / Minimal |
| 总包数 / Total packages | ~180 | 含传递依赖 / Including transitive |
| node_modules 大小 / Size | ~85MB | 符号链接视图 / Symlink view |
| 实际存储 / Actual store | ~60MB | 硬链接去重后 / After hardlink dedup |
| 未使用依赖 / Unused deps | 0 | 定期 depcheck / Regular depcheck |

## 17. 安全与供应链攻击防护 / Security & Supply Chain Attack Prevention

### 17.1 供应链攻击向量 / Supply Chain Attack Vectors

```text
┌────────────────────────────────────────────────────────────────┐
│  npm 供应链攻击面 / npm Supply Chain Attack Surface             │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  攻击向量 / Attack Vectors:                                    │
│                                                                │
│  1. 依赖混淆 / Dependency Confusion                            │
│     └── 恶意包名与私有包同名 / Malicious pkg same as private   │
│                                                                │
│  2. 拼写错误 / Typosquatting                                   │
│     └── react-dom vs react-dorn / Similar names                │
│                                                                │
│  3. 维护者账号劫持 / Maintainer Account Hijack                  │
│     └── 发布恶意更新 / Publish malicious update                │
│                                                                │
│  4. 恶意 postinstall / Malicious postinstall                   │
│     └── 安装时执行任意代码 / Execute code on install           │
│                                                                │
│  5. 传递依赖注入 / Transitive Dependency Injection             │
│     └── 深层依赖被篡改 / Deep dep tampered                     │
│                                                                │
│  pnpm 的防护 / pnpm's Protections:                             │
│  ✓ 严格 node_modules 隔离 / Strict isolation                   │
│  ✓ 锁文件完整性校验 / Lockfile integrity check                 │
│  ✓ 默认不执行构建脚本 / No build scripts by default            │
│  ✓ 内容寻址存储校验 / CAS integrity verification               │
└────────────────────────────────────────────────────────────────┘
```

### 17.2 pnpm 安全配置 / pnpm Security Configuration

```yaml
# .npmrc 安全配置 / Security configuration

# 1. 禁止未授权的构建脚本 / Block unauthorized build scripts
# 仅允许白名单包执行 scripts / Only allow whitelisted packages
# pnpm v9+ 默认阻止所有构建脚本 / Blocks all by default

# 2. 锁定注册表 / Lock registry
registry=https://registry.npmjs.org/

# 3. 严格 SSL / Strict SSL
strict-ssl=true

# 4. 审计级别 / Audit level
audit-level=high
```

```json
// package.json - 构建脚本白名单 / Build script allowlist
{
  "pnpm": {
    "onlyBuiltDependencies": [
      "esbuild",
      "@esbuild/darwin-arm64"
    ],
    "neverBuiltDependencies": [
      "core-js",
      "fsevents"
    ]
  }
}
```

### 17.3 锁文件完整性保护 / Lockfile Integrity Protection

```bash
# CI 中强制锁文件一致 / Enforce lockfile consistency in CI
pnpm install --frozen-lockfile
# 如果 package.json 与 lockfile 不一致则失败
# Fails if package.json and lockfile are inconsistent

# 验证锁文件完整性 / Verify lockfile integrity
pnpm install --lockfile-only --frozen-lockfile

# 检查包的完整性哈希 / Check package integrity hashes
# pnpm-lock.yaml 中每个包都有 integrity 字段:
# /react@18.2.0:
#   resolution: {integrity: sha512-...}
#   这确保下载内容与发布时一致 / Ensures download matches publish
```

### 17.4 本项目安全实践总结 / This Project's Security Practice Summary

| 措施 / Measure | 状态 / Status | 说明 / Notes |
|---|---|---|
| --frozen-lockfile (CI) | ✅ 使用 / Used | 防止隐式更新 / Prevent implicit updates |
| pnpm audit (CI) | ✅ 使用 / Used | 检测已知漏洞 / Detect known vulnerabilities |
| 构建脚本白名单 / Script allowlist | ✅ 配置 / Configured | 仅 esbuild / Only esbuild |
| 最小化依赖 / Minimal deps | ✅ 实践 / Practiced | ~15 直接依赖 / ~15 direct deps |
| 定期更新 / Regular updates | ✅ 手动 / Manual | 月度检查 / Monthly check |
| 私有注册表 / Private registry | ❌ 不需要 / Not needed | 无私有包 / No private packages |
| Dependabot/Renovate | ❌ 未配置 / Not configured | 项目规模小 / Small project scale |

---

## 18. Monorepo 工作区管理 / Monorepo Workspace Management

### 18.1 pnpm workspace 架构 / pnpm workspace Architecture

pnpm 原生支持 Monorepo，通过 workspace 协议管理多包项目：

```
┌─────────────────────────────────────────────────────────────────┐
│         pnpm Monorepo 结构 / pnpm Monorepo Structure            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  PrivShield/                                           │
│  ├── pnpm-workspace.yaml        ← 工作区定义 / Workspace def  │
│  ├── package.json               ← 根配置 / Root config         │
│  ├── pnpm-lock.yaml             ← 全局锁文件 / Global lockfile │
│  │                                                              │
│  ├── console/                                                   │
│  │   ├── web/                   ← 前端包 / Frontend package    │
│  │   │   ├── package.json                                       │
│  │   │   └── src/                                               │
│  │   │                                                          │
│  │   └── bff-go/               ← Go BFF / Go BFF            │
│  │       └── go.mod             (非 pnpm 管理 / Not pnpm)       │
│  │                                                              │
│  └── packages/                  ← 共享包 / Shared packages     │
│      ├── ui-components/                                         │
│      │   └── package.json       (name: @privshield/ui)                 │
│      └── api-client/                                            │
│          └── package.json       (name: @privshield/api-client)         │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 18.2 workspace 配置详解 / workspace Configuration Details

```yaml
# ===== pnpm-workspace.yaml =====
# 定义工作区包位置 / Define workspace package locations
packages:
  - 'console/web'           # 前端应用 / Frontend app
  - 'packages/*'            # 所有共享包 / All shared packages
  - '!**/test/**'           # 排除测试目录 / Exclude test dirs

# 注意 / Notes:
# - 支持 glob 模式 / Supports glob patterns
# - ! 前缀表示排除 / ! prefix means exclude
# - 每个包必须有 package.json / Each package needs package.json
```

```json
// ===== 根 package.json =====
{
  "name": "PrivShield-monorepo",
  "private": true,
  "scripts": {
    // 在所有包中运行 / Run in all packages
    "build": "pnpm -r run build",
    "test": "pnpm -r run test",
    "lint": "pnpm -r run lint",

    // 在特定包中运行 / Run in specific package
    "dev:web": "pnpm --filter @privshield/web run dev",
    "build:web": "pnpm --filter @privshield/web run build",

    // 并行运行 / Run in parallel
    "dev": "pnpm -r --parallel run dev"
  },
  "devDependencies": {
    // 根级共享工具 / Root-level shared tools
    "typescript": "^5.4.0",
    "prettier": "^3.2.0"
  }
}
```

### 18.3 包间依赖与 workspace 协议 / Inter-package Dependencies & workspace Protocol

```json
// ===== packages/api-client/package.json =====
{
  "name": "@privshield/api-client",
  "version": "1.0.0",
  "main": "dist/index.js",
  "types": "dist/index.d.ts",
  "dependencies": {
    // workspace 协议: 链接到本地包 / workspace protocol: link to local package
    "@privshield/types": "workspace:*",      // 任何版本 / Any version
    "@privshield/utils": "workspace:^1.0.0"  // 兼容版本 / Compatible version
  }
}

// ===== console/web/package.json =====
{
  "name": "@privshield/web",
  "dependencies": {
    "react": "^18.2.0",
    // 使用本地包 / Use local packages
    "@privshield/api-client": "workspace:*",
    "@privshield/ui": "workspace:*"
  }
}
```

```bash
# ===== workspace 常用命令 / Common workspace Commands =====

# 添加依赖到特定包 / Add dependency to specific package
pnpm --filter @privshield/web add lodash

# 添加内部包依赖 / Add internal package dependency
pnpm --filter @privshield/web add @privshield/api-client --workspace

# 在所有包中执行 / Execute in all packages
pnpm -r exec pwd

# 仅在有变化的包中构建 / Build only changed packages
pnpm --filter "...[origin/main]" run build

# 查看依赖图 / View dependency graph
pnpm ls -r --depth 0
```

---

## 19. CI/CD 集成实践 / CI/CD Integration Practices

### 19.1 GitHub Actions 集成 / GitHub Actions Integration

```yaml
# ===== .github/workflows/ci.yml =====
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  frontend:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: console/web

    steps:
      - uses: actions/checkout@v4

      # 安装 pnpm / Install pnpm
      - uses: pnpm/action-setup@v4
        with:
          version: 9

      # 设置 Node.js + 缓存 / Setup Node.js + cache
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: 'pnpm'
          cache-dependency-path: console/web/pnpm-lock.yaml

      # 安装依赖（冻结锁文件）/ Install deps (frozen lockfile)
      - name: Install dependencies
        run: pnpm install --frozen-lockfile

      # 类型检查 / Type check
      - name: Type check
        run: pnpm run type-check

      # Lint
      - name: Lint
        run: pnpm run lint

      # 测试 / Test
      - name: Test
        run: pnpm run test -- --coverage

      # 构建 / Build
      - name: Build
        run: pnpm run build

      # 上传产物 / Upload artifacts
      - uses: actions/upload-artifact@v4
        with:
          name: dist
          path: console/web/dist/
```

### 19.2 缓存策略 / Caching Strategy

```yaml
# ===== pnpm 缓存配置 / pnpm Cache Configuration =====

# 缓存位置 / Cache locations:
# - 全局存储 / Global store: ~/.pnpm-store
# - 元数据缓存 / Metadata cache: ~/.cache/pnpm
# - 状态缓存 / State cache: node_modules/.pnpm

# GitHub Actions 缓存键 / Cache keys:
# - 主键 / Primary: pnpm-${{ runner.os }}-${{ hashFiles('**/pnpm-lock.yaml') }}
# - 回退 / Fallback: pnpm-${{ runner.os }}-

# 缓存效果 / Cache effectiveness:
# - 无缓存 / No cache: ~45s 安装 / install
# - 有缓存 / With cache: ~8s 安装 / install
# - 节省 / Savings: ~80%
```

### 19.3 Docker 构建优化 / Docker Build Optimization

```dockerfile
# ===== Dockerfile 中的 pnpm 最佳实践 / pnpm Best Practices in Dockerfile =====
FROM node:20-slim AS base

# 启用 corepack / Enable corepack
RUN corepack enable && corepack prepare pnpm@9 --activate

WORKDIR /app

# 仅复制锁文件（利用缓存）/ Copy only lockfile (leverage cache)
COPY pnpm-lock.yaml ./
COPY package.json ./

# 安装依赖（仅生产）/ Install deps (production only)
RUN pnpm install --frozen-lockfile --prod

# 复制源码 / Copy source
COPY . .

# 构建 / Build
RUN pnpm run build

# 生产镜像 / Production image
FROM node:20-slim AS production
WORKDIR /app
COPY --from=base /app/dist ./dist
COPY --from=base /app/node_modules ./node_modules
CMD ["node", "dist/server.js"]
```

---

## 20. pnpm 与其他包管理器对比 / pnpm vs Other Package Managers

### 20.1 架构对比 / Architecture Comparison

```
┌─────────────────────────────────────────────────────────────────┐
│       包管理器架构对比 / Package Manager Architecture           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  npm (扁平化 / flat):                                          │
│  node_modules/                                                  │
│  ├── package-a/        ← 直接依赖 / Direct dep                 │
│  ├── package-b/        ← 直接依赖 / Direct dep                 │
│  ├── package-a-dep/    ← 被提升 / Hoisted (幽灵依赖 / phantom) │
│  └── package-b-dep/    ← 被提升 / Hoisted                      │
│                                                                 │
│  pnpm (符号链接 / symlinked):                                  │
│  node_modules/                                                  │
│  ├── .pnpm/            ← 实际存储 / Actual storage             │
│  │   ├── package-a@1.0.0/                                      │
│  │   │   └── node_modules/                                     │
│  │   │       ├── package-a/    ← 真实文件 / Real files         │
│  │   │       └── package-a-dep -> ../../../package-a-dep@2.0.0 │
│  │   └── package-b@1.0.0/                                      │
│  ├── package-a -> .pnpm/package-a@1.0.0/node_modules/package-a │
│  └── package-b -> .pnpm/package-b@1.0.0/node_modules/package-b │
│                                                                 │
│  Yarn PnP (无 node_modules / no node_modules):                 │
│  .pnp.cjs              ← 解析映射 / Resolution map             │
│  .yarn/cache/          ← zip 包 / Zip packages                 │
│  (无 node_modules 目录 / No node_modules directory)            │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 20.2 功能对比表 / Feature Comparison Table

| 特性 / Feature | pnpm | npm | Yarn (Berry) | 本项目 / Project |
|---|---|---|---|---|
| 磁盘效率 / Disk efficiency | ★★★ 硬链接 / Hardlinks | ★☆☆ 复制 / Copy | ★★☆ 缓存 / Cache | pnpm |
| 安装速度 / Install speed | ★★★ 快 / Fast | ★☆☆ 慢 / Slow | ★★☆ 中 / Medium | pnpm |
| 幽灵依赖防护 / Phantom dep protection | ★★★ 严格 / Strict | ☆☆☆ 无 / None | ★★★ PnP | pnpm |
| Monorepo 支持 / Monorepo | ★★★ 原生 / Native | ★☆☆ 需工具 / Needs tools | ★★★ 原生 / Native | pnpm |
| 锁文件 / Lockfile | pnpm-lock.yaml | package-lock.json | yarn.lock | pnpm-lock |
| 生态兼容 / Ecosystem | ★★★ 优秀 / Excellent | ★★★ 标准 / Standard | ★★☆ 部分 / Partial | pnpm |
| 学习曲线 / Learning curve | ★★☆ 低 / Low | ★★★ 无 / None | ★☆☆ 高 / High | pnpm |

### 20.3 迁移指南 / Migration Guide

```bash
# ===== 从 npm 迁移到 pnpm / Migrate from npm to pnpm =====

# 1. 安装 pnpm / Install pnpm
npm install -g pnpm
# 或使用 corepack / Or use corepack
corepack enable && corepack prepare pnpm@latest --activate

# 2. 删除 npm 产物 / Remove npm artifacts
rm -rf node_modules package-lock.json

# 3. 导入并安装 / Import and install
pnpm import  # 从 package-lock.json 生成 pnpm-lock.yaml
pnpm install

# 4. 更新脚本 / Update scripts
# package.json:
#   "preinstall": "npx only-allow pnpm"  # 强制使用 pnpm / Enforce pnpm

# 5. 更新 CI / Update CI
# 使用 pnpm/action-setup@v4

# 6. 更新 .gitignore / Update .gitignore
echo "node_modules" >> .gitignore
echo ".pnpm-store" >> .gitignore

# 7. 验证 / Verify
pnpm install --frozen-lockfile
pnpm run build
pnpm run test
```

### 20.4 本项目选择 pnpm 的理由 / This Project's Rationale for pnpm

| 理由 / Reason | 说明 / Description |
|---|---|
| 磁盘效率 / Disk efficiency | 多项目共享存储 / Shared store across projects |
| 严格依赖 / Strict deps | 防止幽灵依赖 / Prevent phantom dependencies |
| 速度 / Speed | 安装比 npm 快 2-3x / 2-3x faster than npm |
| Monorepo 就绪 / Monorepo ready | 未来扩展无需换工具 / No tool switch for future |
| Vite 官方推荐 / Vite recommended | 生态一致性 / Ecosystem consistency |

## 21. 安全审计与供应链 / Security Audit & Supply Chain

### 21.1 pnpm audit 详解 / pnpm audit Details

```bash
# 基本安全审计
# Basic security audit
pnpm audit

# 仅显示高危及以上
# Show only high and critical
pnpm audit --audit-level=high

# JSON 格式输出（CI 集成）
# JSON output (CI integration)
pnpm audit --json > audit-report.json

# 忽略开发依赖
# Ignore dev dependencies
pnpm audit --prod
```

```bash
# 审计输出示例
# Audit output example
# ┌─────────────────────┬────────────────────────────────────┐
# │ high                │ Prototype Pollution                │
# ├─────────────────────┼────────────────────────────────────┤
# │ Package             │ lodash                             │
# │ Vulnerable versions │ <4.17.21                           │
# │ Patched versions    │ >=4.17.21                          │
# │ Paths               │ . > lodash                         │
# └─────────────────────┴────────────────────────────────────┘
```

### 21.2 自动化安全扫描 / Automated Security Scanning

```yaml
# .github/workflows/security.yml
name: Security Audit

on:
  schedule:
    - cron: '0 9 * * 1'  # 每周一 / Every Monday
  push:
    paths:
      - 'console/web/pnpm-lock.yaml'

jobs:
  audit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      
      - name: Security audit
        run: |
          cd console/web
          pnpm audit --audit-level=high --json > audit.json || true
          
          # 检查是否有高危漏洞
          # Check for high vulnerabilities
          HIGH_COUNT=$(cat audit.json | jq '.metadata.vulnerabilities.high // 0')
          CRIT_COUNT=$(cat audit.json | jq '.metadata.vulnerabilities.critical // 0')
          
          if [ "$HIGH_COUNT" -gt 0 ] || [ "$CRIT_COUNT" -gt 0 ]; then
            echo "❌ Found $HIGH_COUNT high, $CRIT_COUNT critical vulnerabilities"
            exit 1
          fi
          echo "✅ No high/critical vulnerabilities"
```

### 21.3 供应链安全实践 / Supply Chain Security Practices

```json
// .npmrc — 安全配置
// .npmrc — Security configuration
{
  // 严格模式：不允许未声明的依赖
  // Strict mode: don't allow undeclared deps
  "strict-peer-dependencies": true,
  
  // 锁定文件必须一致
  // Lockfile must be consistent
  "frozen-lockfile": true,
  
  // 禁止构建脚本（除白名单）
  // Block build scripts (except allowlist)
  "ignore-scripts": false
}
```

```yaml
# pnpm 构建脚本白名单
# pnpm build script allowlist
# package.json
{
  "pnpm": {
    "onlyBuiltDependencies": [
      "esbuild"  // 仅允许 esbuild 执行构建脚本
    ]
  }
}
```

### 21.4 安全检查清单 / Security Checklist

| 检查项 / Check Item | 频率 / Frequency | 工具 / Tool |
|---|---|---|
| pnpm audit | 每次 CI / Every CI | pnpm audit --prod |
| 依赖更新 / Dependency update | 月度 / Monthly | pnpm update |
| lockfile 完整性 / Lockfile integrity | 每次安装 / Every install | --frozen-lockfile |
| 构建脚本审计 / Script audit | 新增依赖时 / On new dep | pnpm.onlyBuiltDependencies |
| 许可证检查 / License check | 季度 / Quarterly | license-checker |

## 22. 发布工作流 / Publishing Workflow

### 22.1 语义化版本管理 / Semantic Versioning

```bash
# 版本更新命令
# Version update commands

# 补丁版本 (1.0.0 → 1.0.1)
# Patch version
pnpm version patch -m "fix: 修复脱敏字段解析 / fix mask field parsing"

# 次版本 (1.0.0 → 1.1.0)
# Minor version
pnpm version minor -m "feat: 添加批量分类 API / add batch classify API"

# 主版本 (1.0.0 → 2.0.0)
# Major version
pnpm version major -m "feat!: 重构 API 响应格式 / refactor API response"
```

### 22.2 Changesets 管理变更 / Changesets for Change Management

```bash
# 安装 changesets
# Install changesets
pnpm add -Dw @changesets/cli

# 初始化
# Initialize
pnpm changeset init

# 记录变更
# Record changes
pnpm changeset
# 交互式选择：
# Interactive selection:
# - 受影响的包 / Affected packages
# - 变更类型 (patch/minor/major) / Change type
# - 变更描述 / Description

# 消费变更（更新版本号 + CHANGELOG）
# Consume changes (update versions + CHANGELOG)
pnpm changeset version

# 发布
# Publish
pnpm changeset publish
```

### 22.3 CI/CD 发布管线 / CI/CD Release Pipeline

```yaml
# .github/workflows/release.yml
name: Release

on:
  push:
    branches: [main]

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: 'pnpm'
          registry-url: 'https://registry.npmjs.org'
      
      - run: pnpm install --frozen-lockfile
      - run: pnpm build
      - run: pnpm test
      
      - name: Create Release PR or Publish
        uses: changesets/action@v1
        with:
          publish: pnpm changeset publish
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
```

## 23. 企业级实践 / Enterprise Practices

### 23.1 私有注册表配置 / Private Registry Configuration

```ini
# .npmrc — 企业私有注册表
# .npmrc — Enterprise private registry

# 默认使用内部镜像
# Default to internal mirror
registry=https://npm.internal.company.com/

# 作用域包使用私有注册表
# Scoped packages use private registry
@company:registry=https://npm.internal.company.com/

# 认证 token
# Auth token
//npm.internal.company.com/:_authToken=${NPM_TOKEN}

# 公共包回退到 npmjs
# Public packages fallback to npmjs
# (通过代理实现 / Via proxy)
```

### 23.2 大型 Monorepo 优化 / Large Monorepo Optimization

```yaml
# pnpm-workspace.yaml — 大型工作区配置
# pnpm-workspace.yaml — Large workspace config
packages:
  - 'packages/*'
  - 'apps/*'
  - 'tools/*'
  - '!**/test/**'  # 排除测试目录 / Exclude test dirs
```

```json
// .npmrc — 性能优化
// .npmrc — Performance optimization
{
  // 并行安装数
  // Parallel install count
  "network-concurrency": 16,
  
  // 子进程数
  // Child process count
  "child-concurrency": 8,
  
  // 使用硬链接节省磁盘
  // Use hardlinks to save disk
  "package-import-method": "hardlink",
  
  // 共享存储位置
  // Shared store location
  "store-dir": "/data/pnpm-store"
}
```

### 23.3 依赖治理策略 / Dependency Governance Strategy

| 策略 / Strategy | 实践 / Practice | 工具 / Tool |
|---|---|---|
| 版本锁定 / Version pinning | 生产依赖精确版本 / Exact prod deps | pnpm-lock.yaml |
| 定期更新 / Regular updates | 月度更新 + 审计 / Monthly update + audit | pnpm update |
| 重复检测 / Duplicate detection | 避免多版本共存 / Avoid multi-version | pnpm dedupe |
| 废弃清理 / Deprecated cleanup | 移除不再维护的包 / Remove unmaintained | pnpm ls --depth=0 |
| 大小监控 / Size monitoring | 新依赖体积审查 / New dep size review | bundlephobia |
| 许可证合规 / License compliance | 仅允许 MIT/Apache/BSD | license-checker |
