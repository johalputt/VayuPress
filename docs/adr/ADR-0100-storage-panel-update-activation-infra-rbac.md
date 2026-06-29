# ADR-0100: Storage & System Panel, Reliable Self-Update Activation, and Admin-Only Infrastructure Detail

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt  
**Relates to**: [ADR-0099](ADR-0099-self-contained-one-click-update.md), [ADR-0098](ADR-0098-role-scoped-vayuos-access.md), [ADR-0089](ADR-0089-vayuos-one-click-update-and-backup.md)

## Context

Three operability gaps surfaced from real operator use:

1. **Disk filled up with no in-app visibility.** VayuPress accumulates artefacts
   over time — automatic pre-update database backups, the update script's
   timestamped snapshots, log files, temporary export files — but the only way
   to see or remove them was the shell. An operator could not tell how much RAM
   or disk (NVMe) the system was using, or reclaim space, from VayuOS.

2. **A one-click update "succeeded" but the old version kept running.** After the
   binary was swapped, the in-process restart re-derived its target from
   `os.Executable()`. Immediately after a self-replace the kernel reports that
   path as `"/usr/local/bin/vayupress (deleted)"` (the old inode was unlinked by
   the atomic swap), so `execve` either failed — falling through to a supervisor
   restart of the *old* `ExecStart` — or re-ran the stale image. The operator saw
   "restarting, refresh shortly", refreshed, and was still on the old version and
   prompted to update again.

3. **Infrastructure detail was visible to non-admin roles.** The PGP key
   registry, the DKIM/SPF/DMARC records and live DNS health, and the dependency
   security-update watcher were reachable by every console role. The four
   non-admin roles (editor, author, reviewer, mailbox) do not need this and it is
   sensitive operational surface.

## Decision

### 1. Storage & System panel (administrators only)

A new admin-only page at `/os/storage`:

- **Resource usage** — system RAM (used/total from `/proc/meminfo`), this
  process's resident memory (`/proc/self/status` VmRSS) and Go heap, and disk
  usage of the filesystem holding the database (`statfs`), plus the on-disk
  footprint of the database (+WAL/SHM), render cache, media, and pre-update
  backups.
- **Managed files** — database backups, log files and temporary files listed
  with size and age, each with a one-click **Download** and **Delete** (single or
  bulk). Download lifts the write deadline so large backups stream in constant
  memory.

**Security:** every endpoint is admin-role gated and writes are CSRF-protected.
Download/delete never trust a client path: the requested path must exactly match
a file currently enumerated by `managedStorageFiles()` (re-scanned per call), so
path traversal is impossible and the live database / WAL can never be served or
removed. Listing is one level deep over a fixed set of roots
(`CACHE_DIR/update-backups`, the DB directory's backup files, `VAYU_LOG_DIR`,
`VAYU_BACKUP_DIR`, `TMP_DIR`) and only ever returns regular files.

### 2. Reliable self-update activation

The restart now re-execs the **exact binary path the update just wrote**
(captured *before* the swap, so it is the clean install path), not a
re-derivation of `os.Executable()` after the swap. A new `RelaunchExec(path)` /
`ScheduleRestartExec(path)` take that path; `cleanExePath` additionally strips a
trailing `" (deleted)"` marker and resolves symlinks as defence-in-depth for the
no-known-path callers (the restore/restart endpoints). The apply and rollback
handlers pass the freshly-written path, so the new code runs in-process with the
PID preserved — independent of any supervisor.

A **writability preflight** runs before download: if the binary's directory is
not writable (most often a systemd `ProtectSystem=strict/full` sandbox making
`/usr/local/bin` read-only), apply fails fast with an actionable message
(make the directory writable via `ReadWritePaths=`, or update from the shell)
instead of a confusing mid-update failure.

### 3. Admin-only infrastructure detail

`handleVayuOSPGP`, `handleVayuOSMail` (DNS records / health / deliverability) and
`handleVayuOSSecurity` now refuse non-admins (redirect to the inbox). The VayuMail
sub-navigation (`vayuosNav`) and the dashboard cards omit PGP, DNS, Security and
account-management for non-admins, so those four roles see only the mail surface
they use (Overview, Compose, Mailbox, Connect, Outbox). This complements the
route-level access model from ADR-0098.

## Consequences

- Operators can see and reclaim disk from VayuOS without shell access, and can
  always answer "how much RAM/disk is VayuPress using?".
- A one-click update reliably activates the new version in-process; the
  "still old version after refresh" loop is closed, and an unwritable binary
  location is reported clearly rather than failing silently.
- Sensitive PGP/DNS/security surface is administrator-only, hidden and
  unreachable for the four non-admin roles.
- The Storage panel reads `/proc` and uses `statfs`, consistent with the
  Linux-only deployment target (the self-restart already relies on `execve`).
