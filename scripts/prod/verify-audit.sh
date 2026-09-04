#!/usr/bin/env bash
# ============================================================================
# 审计日志 HMAC-SHA256 签名完整性校验脚本
# Verify Audit Log HMAC-SHA256 Signature Integrity
#
# 本脚本通过 scripts/prod/verify_audit.go 执行高性能纯 Go 校验，
# 用于校验 BudgetAuditLogger 写入的审计日志签名是否完整、未被篡改。
#
# 用法 / Usage:
#   bash scripts/prod/verify-audit.sh --key <HMAC_KEY> [--log-file <PATH>]
#   PRIVACY_AUDIT_KEY=<key> bash scripts/prod/verify-audit.sh
#
# 退出码 / Exit codes:
#   0 - 所有记录签名校验通过
#   1 - 存在签名不匹配或格式错误的记录
#   2 - 参数错误或文件不存在
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# ── 审计日志签名校验 ───────────────────────────────────────────────────
cd "$PROJECT_ROOT"
exec go run "$PROJECT_ROOT/scripts/prod/verify_audit.go" "$@"
