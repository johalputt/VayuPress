#!/bin/bash
set -euo pipefail
DB="${VAYUPRESS_DB:-/var/lib/vayupress/vayupress.db}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/internal/migrations/sql}"
# Backup before migrating
BACKUP="${DB}.bak.$(date +%Y%m%d%H%M%S)"
sqlite3 "$DB" ".backup $BACKUP" && echo "✅ backup: $BACKUP"
# Retention: keep only the newest N timestamped backups next to the DB so they
# cannot accumulate without bound across repeated migrations/deploys (tunable
# via VAYUPRESS_BACKUP_KEEP; default 5). Best-effort — never abort a migration
# because pruning failed.
KEEP="${VAYUPRESS_BACKUP_KEEP:-5}"
mapfile -t _baks < <(ls -1t "${DB}".bak.* 2>/dev/null || true)
if [ "${#_baks[@]}" -gt "$KEEP" ]; then
  for _old in "${_baks[@]:$KEEP}"; do
    rm -f -- "$_old" && echo "🧹 pruned old backup: $_old"
  done
fi
# Apply each .up.sql file in order
for f in "$MIGRATIONS_DIR"/*.up.sql; do
  [ -f "$f" ] || continue
  echo "Applying: $f"
  sqlite3 "$DB" < "$f" && echo "✅ applied: $(basename "$f")"
done
echo "✅ migrations complete"
