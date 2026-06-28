# ADR-0099: Self-Contained One-Click Update — Embedded Admin Assets + Arch-Aware Release Selection

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt  
**Extends**: [ADR-0089](ADR-0089-vayuos-one-click-update-and-backup.md), [ADR-0064](ADR-0064-sovereign-self-update.md)

## Context

ADR-0089 shipped the VayuOS "Update & Backup" panel: a one-click, checksum- (and
optionally signature-) verified self-update plus a full database + settings
backup/export/import. The self-update atomically replaces the running binary and
re-execs to activate it.

Two gaps stopped it from being a *complete* one-click update in production:

1. **Stale admin assets after a binary-only update.** The VayuOS admin CSS/JS
   (`admin-os.css` and every `admin-os-*.js`) were served straight from
   `STATIC_DIR` on disk and refreshed by a *separate* file-copy step in the
   deploy script (`scripts/update-vayupress.sh`). The self-update replaces only
   the executable, so after a one-click update the panel kept loading the *old*
   CSS/JS until someone manually re-copied `static/`. A new binary paired with
   stale assets is a half-applied update — broken layouts, JS referencing
   removed endpoints — which directly contradicts "update the binary, update
   everything."

2. **Fragile release-asset selection.** The updater picked the binary as "the
   first release asset that does not end in `.sig`/`.sha256`." A release also
   carries a `*.cosign.bundle`, which matches that rule, so depending on the
   order the GitHub API returns assets the updater could try to install the
   signature bundle as the binary (it would then fail checksum verification —
   safe, but the update would never succeed). It was also not OS/arch-aware, so
   it could not support multi-platform releases.

## Decision

Make the binary the single source of truth for first-party assets, and make
release-asset selection explicit and platform-aware.

### Embedded admin assets, synced on boot

- The repository `static/` tree is compiled into the binary via a module-root
  `//go:embed static` (`embedded_assets.go`, package `vayupress`).
- On startup, **before** `render.Init`, `syncEmbeddedStatic(STATIC_DIR)` writes
  every embedded asset to disk, rewriting a file only when its bytes differ from
  what is already there. Running before `render.Init` lets the renderer keep
  authority over the four minified public-site CSS files (`article`/`admin`/
  `high-contrast`/`custom`) while this refreshes everything else — crucially
  `admin-os.css` and all `admin-os-*.js`. Files VayuPress does not ship are left
  untouched; unchanged files are skipped so content-hash cache-busters stay
  stable.
- `serveAdminOSAsset` and `assetVer` fall back to the embedded copy when the
  on-disk file is absent, so the panel still works even if `STATIC_DIR` is
  unprovisioned or read-only under a hardened service sandbox.

Net effect: the new binary *carries* the new admin assets and lays them down on
the first boot after a self-update — no extra step, no stale-asset window. The
existing deploy-script copy step remains valid but is now redundant for these
assets.

### OS/arch-aware, sidecar-skipping release selection

`internal/update/apply.go` replaces the position-based `findAsset` with explicit
selectors:

- `selectBinaryAsset(assets, GOOS, GOARCH)` discards known sidecars
  (`.sha256`, `.sig`, `.asc`, `.cosign.bundle`, SBOMs, notes, …), then — when a
  release ships multiple platform builds — prefers the asset whose name encodes
  the running `GOOS` and `GOARCH` (with common arch aliases: `amd64`↔`x86_64`,
  `arm64`↔`aarch64`, …). A single remaining candidate is returned as-is, so
  VayuPress's own single-binary releases are unaffected.
- `selectChecksumAsset` / `selectSignatureAsset` prefer an exact
  `<binary>.sha256` / `<binary>.sig` sibling and fall back to the sole such
  asset, refusing (rather than guessing) when several exist with no exact match.

Verification policy is unchanged (checksum always; Ed25519 signature when a
release key is pinned).

## Consequences

- A one-click self-update is now genuinely complete: binary, embedded migrations
  (already run on boot), and admin CSS/JS all advance together, atomically, with
  the automatic pre-update database backup and re-exec from ADR-0089.
- The binary grows by the size of `static/` (~0.6 MB). Acceptable for a sovereign
  single-binary product, and it removes a class of "forgot to copy static"
  deploy bugs.
- The updater can now drive correct multi-platform releases if VayuPress ever
  ships them, and can never mistake a signature bundle for the executable.
- Operator-customized public theming is unaffected: custom CSS lives in the
  database (injected on public pages), not in a hand-edited `STATIC_DIR` file.
