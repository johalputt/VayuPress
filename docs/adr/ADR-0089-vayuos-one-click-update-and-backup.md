# ADR-0089: VayuOS One-Click Update & Full Backup/Restore

**Status**: Accepted  
**Date**: 2026-06-26  
**Author**: @johalputt

## Context

VayuPress already shipped a secure self-update engine (`internal/update`,
ADR-0064): it checks GitHub for a newer release, downloads the binary plus its
`.sha256` and `.sig`, verifies the SHA-256 checksum **and** an Ed25519 signature
against a pinned release public key, backs up the database, and atomically
replaces the running binary. It also shipped a database backup helper
(`CreateBackup`) that archives the SQLite file and its WAL/SHM sidecars.

Two gaps remained for a single-VPS operator who never wants to touch a shell:

1. **The apply path was CLI-only.** `vayupress update apply` required SSH
   access, and it never restarted the process — the operator still had to run
   `systemctl restart`. There was no way to update from VayuOS.
2. **There was no whole-site export/import.** `CreateBackup` produced an archive
   on the server's disk but offered no download, no upload-and-restore, and no
   consistency guarantee against a live WAL. Moving a site between machines, or
   rolling back content+settings, meant manual file surgery.

The request: a production-grade VayuOS surface where the operator updates
VayuPress in one click — binary and everything — with no command line and no
half-finished state, and can back up the entire database and settings to one
compressed file and import/export it again, with no size limit.

## Decision

### 1. Expose the existing engine through VayuOS, keeping every gate

A new **Update & Backup** panel (`/os/update`, `admin_os_update.go`) drives the
unchanged `internal/update` engine. The security contract of ADR-0064 is
preserved exactly:

- Apply still calls `PreflightApply`: it refuses unless
  `VAYU_SELFUPDATE_ENABLED=true`, a pinned `VAYU_RELEASE_PUBKEY` is present, and
  the system is not in read-only / quarantined / maintenance mode.
- The release is verified (checksum + Ed25519) **before** any byte is written,
  and the database is backed up before the binary is swapped.

These are one-time deployment settings, not per-update steps, so once armed the
operator truly gets one click. Every action is **admin-role gated**,
**CSRF-protected** on writes, and recorded in both the WORM `audit_log` and the
`update_history` table.

### 2. Finish the job: in-process self-restart

`internal/update/restart.go` adds `Relaunch` / `ScheduleRestart`, which re-exec
the freshly written binary in place via `execve(2)` after a graceful
`PRAGMA wal_checkpoint(TRUNCATE)` and DB close. The PID and service identity are
preserved; if the re-exec ever fails, the process exits cleanly so a supervisor
(systemd `Restart=always`, Docker restart policy) brings it back. This is the
piece that makes the update truly hands-free — no `systemctl restart` step.

### 3. Whole-site snapshots: consistent, streamed, unbounded

`internal/update/snapshot.go` adds a self-describing snapshot format — a single
`.tar.gz` containing `manifest.json`, a portable `settings.json` dump, and the
database (`vayupress.db`). Because all site settings live in the `site_settings`
table, one DB copy captures content **and** settings.

- **Consistency:** the DB is copied with SQLite `VACUUM INTO`, which takes a read
  snapshot and writes a fully checkpointed, defragmented standalone file. No
  torn pages, and no need to ship `-wal`/`-shm` sidecars.
- **No size limit, constant memory:** export streams `manifest → settings → DB`
  straight to the HTTP response with `io.Copy`; import streams the upload
  through `gzip`/`tar` readers to disk via a `MultipartReader` (never
  `ParseMultipartForm`). The handlers lift the server read/write deadlines with
  `http.ResponseController` so large transfers don't time out.
- **Integrity:** the manifest records the DB SHA-256; restore recomputes and
  compares it, then validates the staged file is a real, intact VayuPress
  database (`PRAGMA integrity_check` + presence of `schema_migrations`,
  `articles`, `site_settings`).

### 4. Crash-safe restore via a startup swap

A restore never mutates the open live database. The validated incoming DB is
staged as `<db>.pending-restore`; `ApplyPendingRestore` — called at process
start **before** the DB is opened — takes an automatic safety backup of the
current DB, then atomically renames the staged file into place and clears stale
sidecars. The import handler triggers a restart to complete it. The swap is
therefore atomic and recoverable even if the process dies mid-restore.

## Consequences

- Operators update, roll back, back up and restore a VayuPress site entirely
  from the browser, with no shell access and no manual restart.
- The self-update threat model is unchanged from ADR-0064: signatures are still
  mandatory, the opt-in env gate still applies, and unsafe modes still refuse.
- Self-restart relies on `execve(2)` and is Linux-only, matching VayuPress's
  supported deployment target (Dockerfile / `deploy/`). On any re-exec failure
  the process exits for a supervisor to restart it, so there is no stuck state.
- A restore replaces all content and settings; the pre-restore safety backup in
  the update-backups directory is the recovery path if a wrong file is imported.
- No new third-party dependencies: snapshots use only `archive/tar`,
  `compress/gzip`, `database/sql`, and `crypto/sha256`.
