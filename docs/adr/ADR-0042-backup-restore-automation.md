# ADR-0042: Backup Restore Automation + Checksum Registry

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

A backup that has never been tested is not a backup — it's a hope. VayuPress backs up the SQLite database nightly, but without automated restore validation, backup corruption or format drift may go undetected until a real restore is needed.

## Decision

1. Nightly backup (02:00 UTC): `sqlite3 /data/vayupress.db ".backup /data/backups/vayupress-$(date +%Y%m%d).db"` + gzip + SHA-256 checksum appended to `/data/backups/checksums.sha256`.
2. Weekly restore validation (Sunday 03:00 UTC): restore the most recent backup to a temp file, run `PRAGMA integrity_check`, verify checksum against registry, delete temp file.
3. The checksum registry (`/data/backups/checksums.sha256`) is an append-only log: `<sha256>  <filename>  <timestamp>`.
4. Restore validation result is posted to the `audit_log` table: success or failure with filename and checksum.
5. The admin health endpoint `/health/dependencies` includes `"backup":{"last_ok":"2026-06-12T03:00:00Z","status":"ok"}`.
6. Backups older than 30 days are automatically pruned after successful weekly validation.

## Rationale

Automated restore validation closes the feedback loop on backup integrity. The checksum registry enables out-of-band verification (an operator can check the registry from a different machine). Integration with audit_log makes backup health observable via existing log infrastructure.

## Consequences

- Positive: Backup corruption is detected within 7 days, not at incident time.
- Positive: Operator has cryptographic proof that each backup is intact.
- Negative: Weekly restore validation briefly doubles disk usage (original + temp restore). At typical blog sizes (< 1 GB), this is acceptable.
- Negative: The checksum registry file grows unboundedly. Mitigated by the 30-day backup pruning (corresponding registry entries are archived).
