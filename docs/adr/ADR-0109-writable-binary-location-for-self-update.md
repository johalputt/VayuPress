# ADR-0109: Run the Binary from a Service-Owned Writable Directory for Self-Update

**Status**: Accepted  
**Date**: 2026-07-04  
**Author**: @johalputt  
**Relates to**: [ADR-0089](ADR-0089-vayuos-one-click-update-and-backup.md) (one-click update & backup), [ADR-0099](ADR-0099-self-contained-one-click-update.md) (self-contained one-click update)

## Context

The in-app one-click updater (ADR-0089/0099) verifies a release, backs up the
database, then **atomically swaps the running binary** (`os.Rename` of a
freshly-written temp file over the executable). `os.Rename` needs write
permission on the *directory* that holds the binary.

The reference deployment installed the binary at `/usr/local/bin/vayupress` and
ran the service as a non-root user (`www-data`). Two independent layers then
block the swap:

1. **Unix ownership (DAC).** `/usr/local/bin` is `root:root`. A non-root service
   cannot create or rename files there, full stop.
2. **systemd sandbox (MAC).** `ProtectSystem=strict`/`full` mounts `/usr` (and
   thus `/usr/local/bin`) read-only for the unit.

The unit tried to fix this with `ReadWritePaths=/usr/local/bin`, but that only
relaxes layer 2. Layer 1 still denies the write, so every in-app update failed
with *"permission denied writing to /usr/local/bin"*. The advice the UI then
printed — "add it to ReadWritePaths" — was therefore incomplete and misleading.

## Decision

**Run the binary from a service-owned, writable directory so the atomic
self-swap needs no elevated permission and no sandbox exception.**

1. The binary is installed to **`/var/lib/vayupress/bin/vayupress`** — a
   subdirectory of the state directory that is already writable and is
   `chown`ed to the service user. `systemd`'s `ExecStart` points at this path.
2. **`/usr/local/bin/vayupress` becomes a symlink** to that file, so shell
   commands and `scripts/update-vayupress.sh` still resolve `vayupress` on
   `PATH`. `ReadWritePaths` no longer lists `/usr/local/bin` (it never helped).
3. The updater is made **symlink-aware**: `update.ResolveInstallPath` follows
   symlinks (and strips a `(deleted)` marker) so the swap, the `.bak` rollback
   artifact, the writability preflight, and the post-update re-exec all target
   the **resolved real file** in the writable directory — never the symlink.
   Because Linux resolves the `ExecStart` symlink at launch, `os.Executable()`
   already reports the writable target, so no ownership or sandbox change is
   ever required for updates to work.
4. `scripts/deploy-vayupress.sh` and `scripts/update-vayupress.sh` install into
   this layout and **migrate legacy installs** (replace a real
   `/usr/local/bin/vayupress` with the symlink; install a systemd drop-in that
   repoints `ExecStart` at the writable path).
5. When the binary still sits in a directory the service cannot write, the UI
   now states the **real cause and the permanent fix** (run from a service-owned
   directory / re-run the installer) instead of the incomplete
   ReadWritePaths advice.

## Consequences

- Positive: one-click updates work out of the box on a hardened, non-root,
  `ProtectSystem=strict` deployment — no manual `chown`, no sandbox relaxation,
  no root-writable binary directory. Rollback (`.bak`) and re-exec follow the
  same resolved path, so they stay correct under the symlink layout.
- Positive: keeping the binary out of a root-owned path is also better security
  posture — an update never needs write access to a system directory.
- Trade-off: existing installs must re-run the installer (or the shell updater)
  once to migrate to the symlink layout; until then the UI explains the manual
  move. The change is backward-compatible — a single-file, non-symlinked layout
  still swaps in place exactly as before whenever that directory is writable.

## Amendment 2026-07-05 — directory ownership, and a development channel

Two follow-ups after real-world deployment:

- **The binary *directory* must be owned by the service user, not just the
  file.** The atomic swap creates a temp file in the directory and renames it
  over the binary, so `os.Rename` needs write permission on the **directory**
  (DAC). The v2.9.3 installers chowned only the binary file, so a root-owned
  `/var/lib/vayupress/bin` still blocked the swap. Fixed: both installers now
  `chown` the directory to the service user; the reference unit adds
  `StateDirectory=vayupress` (systemd owns the state dir as the service user on
  every start); and the in-app error prints the exact `chown` one-liner.
- **Optional development channel.** `update.CheckLatestChannel` and
  `ApplyOptions.IncludePrerelease` add an opt-in path that also considers GitHub
  pre-releases (unreleased builds), surfaced as a checkbox on the Update panel.
  Verification is unchanged; the stable channel remains the default.
