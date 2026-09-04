#!/usr/bin/env bash
# ==============================================================================
# PrivShield SQLite -> PostgreSQL Phase B Migration Wrapper
# ==============================================================================
# Wraps pkg/store/cmd/migrate so it can be invoked from scripts/prod.
#
# Usage:
#   bash scripts/prod/migrate-sqlite-to-postgres.sh \
#     --hub-db ./data/service-hub.db \
#     --audit-db ./data/audit-log.db \
#     --pg-dsn "postgres://user:pass@localhost:5432/privshield?sslmode=disable" \
#     --batch 500 \
#     --dry-run \
#     --verify
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

cd "${ROOT_DIR}"

# Convert long options to the underlying Go flags.
GO_ARGS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --hub-db) GO_ARGS+=("-hub-db" "$2"); shift 2 ;;
        --audit-db) GO_ARGS+=("-audit-db" "$2"); shift 2 ;;
        --pg-dsn) GO_ARGS+=("-pg-dsn" "$2"); shift 2 ;;
        --batch) GO_ARGS+=("-batch" "$2"); shift 2 ;;
        --dry-run) GO_ARGS+=("-dry-run"); shift ;;
        --verify) GO_ARGS+=("-verify"); shift ;;
        -h|--help) GO_ARGS+=("-h"); shift ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

exec go run ./pkg/store/cmd/migrate "${GO_ARGS[@]}"
