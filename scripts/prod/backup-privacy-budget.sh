#!/usr/bin/env bash
# ============================================================================
# 【生产模式】PrivShield 隐私预算持久化数据库在线热备份与归档工具
# Online Hot Backup & Archival Tool for Privacy Budget Database
#
# 执行步骤总览：
#   1. 解析命令行参数（--db-path、--backup-dir、--keep-days）
#   2. 检查源 SQLite 数据库文件存在性
#   3. 创建备份归档目录
#   4. 调用 SQLite Online Backup API 执行无锁安全热备份
#   5. Gzip -9 高比例压缩备份文件并计算 SHA256 校验和
#   6. 自动轮转扫描并清理超过保留天数的过期备份文件
#
# 用法 / Usage:
#   ./scripts/prod/backup-privacy-budget.sh [选项]
#
# 选项 / Options:
#   --db-path PATH        待备份的 SQLite 数据库路径 (默认: .data/privacy_budget.db)
#   --backup-dir DIR      备份存储目录 (默认: .data/backups)
#   --keep-days DAYS      备份保留天数 (默认: 7 天，自动清理过期备份)
#   -h, --help            显示帮助信息
# ============================================================================

set -euo pipefail

# ── 步骤 1：定位项目路径与默认参数初始化 ──────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

DB_PATH="${PRIVACY_BUDGET_DB:-$PROJECT_ROOT/.data/privacy_budget.db}"
BACKUP_DIR="$PROJECT_ROOT/.data/backups"
KEEP_DAYS=7

# ── 步骤 2：解析命令行参数 ────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --db-path) DB_PATH="$2"; shift 2 ;;
        --backup-dir) BACKUP_DIR="$2"; shift 2 ;;
        --keep-days) KEEP_DAYS="$2"; shift 2 ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  --db-path PATH        待备份 SQLite 数据库文件路径"
            echo "  --backup-dir DIR      备份产物保存目录"
            echo "  --keep-days DAYS      备份保留天数 (默认 7 天)"
            echo "  -h, --help            显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "❌ [错误] 未知参数: $1" >&2
            exit 1
            ;;
    esac
done

echo "============================================================================"
echo "💾 【生产模式】PrivShield 隐私预算数据库在线热备份"
echo "============================================================================"
echo "  • 源数据库路径 : $DB_PATH"
echo "  • 备份目标目录 : $BACKUP_DIR"
echo "  • 归档保留天数 : $KEEP_DAYS 天"

# ── 步骤 3：检查源数据库文件 ──────────────────────────────────────────────
if [[ ! -f "$DB_PATH" ]]; then
    echo "⚠️  源数据库文件不存在: $DB_PATH (如当前运行在内存模式，无需备份)。"
    exit 0
fi

# ── 步骤 4：准备备份目录与时间戳命名 ──────────────────────────────────────
mkdir -p "$BACKUP_DIR"

TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
TEMP_BACKUP="$BACKUP_DIR/privacy_budget_${TIMESTAMP}.db"
FINAL_BACKUP="$TEMP_BACKUP.gz"

echo ""
echo "⏳ 正在通过 SQLite Online Backup API 执行热备份（无读写锁阻塞）..."

# ── 步骤 5：使用 Python sqlite3 执行在线安全备份 ──────────────────────────
python3 -c "
import sqlite3, sys

src_path = '$DB_PATH'
dst_path = '$TEMP_BACKUP'

try:
    src_conn = sqlite3.connect(src_path, timeout=5.0)
    dst_conn = sqlite3.connect(dst_path)
    with dst_conn:
        src_conn.backup(dst_conn, pages=100, sleep=0.01)
    dst_conn.close()
    src_conn.close()
    print('✅ SQLite 在线备份完成')
except Exception as e:
    print(f'❌ 备份失败: {e}', file=sys.stderr)
    sys.exit(1)
"

# ── 步骤 6：Gzip 压缩与 SHA256 校验和生成 ─────────────────────────────────
echo "📦 正在压缩备份并生成 SHA256 校验和..."
gzip -9 "$TEMP_BACKUP"

if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$FINAL_BACKUP" > "$FINAL_BACKUP.sha256"
elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$FINAL_BACKUP" > "$FINAL_BACKUP.sha256"
fi

BACKUP_SIZE=$(ls -lh "$FINAL_BACKUP" | awk '{print $5}')
echo "✅ 备份文件已生成: $FINAL_BACKUP (大小: $BACKUP_SIZE)"

# ── 步骤 7：自动轮转清理过期备份 ──────────────────────────────────────────
echo ""
echo "🧹 正在清理超过 $KEEP_DAYS 天的旧备份..."
find "$BACKUP_DIR" -name "privacy_budget_*.db.gz*" -type f -mtime "+$KEEP_DAYS" -exec rm -f {} +
echo "✅ 旧备份清理完成。"

echo "============================================================================"
