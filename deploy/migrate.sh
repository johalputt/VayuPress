#!/bin/bash
set -euo pipefail
DB="${VAYUPRESS_DB:-/var/lib/vayupress/vayupress.db}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/migrations/sql}"
# Backup before migrating
BACKUP="${DB}.bak.$(date +%Y%m%d%H%M%S)"
sqlite3 "$DB" ".backup $BACKUP" && echo "✅ backup: $BACKUP"
# Apply each .up.sql file in order
for f in "$MIGRATIONS_DIR"/*.up.sql; do
  [ -f "$f" ] || continue
  echo "Applying: $f"
  sqlite3 "$DB" < "$f" && echo "✅ applied: $(basename "$f")"
done
echo "✅ migrations complete"
