# Upgrading VayuPress

VayuPress supports two upgrade paths. Both are sovereign — nothing phones home,
and the privilege to replace the running binary always requires shell access to
the host.

> **Security model:** see [ADR-0064](adr/ADR-0064-sovereign-self-update.md). In
> short: the web panel can only *check* for updates (read-only). *Applying* an
> update is a CLI action, gated by an opt-in flag, system mode, and an
> **operator-pinned Ed25519 signature** — not just a checksum.

---

## 1. Checking for updates

### From the admin panel (read-only)

`Settings → Software updates → Check for updates` (Admin v2) calls
`GET /admin/api/updates/check`, which compares your running version with the
latest GitHub release and renders the changelog. When an update is available it
surfaces the exact, copy-to-clipboard signed-apply commands (dry-run, apply,
rollback). It performs no download and changes nothing on disk. The page also
shows recent **update history** (`GET /admin/api/updates/history`), and every
check is recorded in the `update_history` table.

### From the CLI

```bash
vayupress update check
```

Prints the current version, the latest release, whether an update is available,
and the release notes.

---

## 2. Applying an update (CLI-only, signature-verified)

Applying is **off by default** and refuses to run unless every safety gate is
satisfied.

### One-time setup

1. **Opt in:**
   ```bash
   export VAYU_SELFUPDATE_ENABLED=true
   ```
2. **Pin the release signing key** (obtain it out-of-band from the project's
   published key, *not* over the same channel as the binary):
   ```bash
   export VAYU_RELEASE_PUBKEY=<hex-encoded-ed25519-public-key>
   ```

### Dry run first (verifies everything, changes nothing)

```bash
vayupress update apply --dry-run
```

This downloads the candidate binary, verifies its SHA-256 against the published
checksum, verifies the **Ed25519 signature** over that digest against your pinned
key, and reports success — without replacing anything.

### Apply

```bash
vayupress update apply
```

Sequence enforced by `internal/update`:

1. Refuse unless `VAYU_SELFUPDATE_ENABLED=true`.
2. Refuse unless `VAYU_RELEASE_PUBKEY` is set.
3. Refuse if system mode is `read-only`, `quarantined`, or `maintenance`.
4. Download → verify checksum (constant-time) → verify Ed25519 signature.
5. Back up the SQLite database (+ WAL/SHM) to a timestamped `.tar.gz`.
6. Atomically swap the binary (`os.Rename`), keeping `<binary>.bak`.
7. **Print restart instructions — does not restart for you.**

### Restart

VayuPress never restarts itself. After a successful apply:

```bash
sudo systemctl restart vayupress
```

### Rollback

The previous binary is preserved as `<binary>.bak` and a database backup is
written before every apply. The fastest path is the built-in command, which
swaps the `.bak` back over the running binary:

```bash
sudo systemctl stop vayupress
vayupress update rollback
sudo systemctl start vayupress
```

Or do it by hand:

```bash
sudo systemctl stop vayupress
mv /usr/local/bin/vayupress.bak /usr/local/bin/vayupress
# restore the DB backup if a migration changed schema:
#   tar xzf /var/cache/vayupress/backups/<timestamp>.tar.gz -C /
sudo systemctl start vayupress
```

### History

```bash
vayupress update history
```

Shows the last update attempts with status (`checked`, `started`, `success`,
`failed`, `rolled_back`), versions, and backup paths.

---

## 3. Manual upgrade (no self-update)

If you prefer not to enable self-update at all, upgrade the operator-driven way:

```bash
# Back up first
vayu-backup backup --db /var/lib/vayupress/data.db --out backup.tar.gz --compress

# Replace the binary with a release you built/verified yourself, then:
sudo systemctl restart vayupress
```

Database migrations run automatically on startup with checksum-drift detection
(ADR-0034) — a tampered or skipped migration halts boot rather than corrupting
data.

---

## Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `VAYU_SELFUPDATE_ENABLED` | `false` | Master opt-in for `update apply` |
| `VAYU_RELEASE_PUBKEY` | *(unset)* | Hex Ed25519 key the apply step verifies against |

Both are required for `apply`; neither affects `check`.

---

## 4. Migrating content from another platform

VayuPress v1.1.0 ships a built-in Markdown import subcommand:

```bash
# Preview (no writes)
vayupress migrate markdown --dir ./posts --dry-run

# Import
vayupress migrate markdown --dir ./posts

# See all migration options (WordPress, Ghost, Hugo, Jekyll, Medium, Notion, Substack)
vayupress migrate info
```

See [docs/MIGRATION.md](MIGRATION.md) for the complete guide.

---

## Release History

| Version | Date | Highlights |
|---------|------|------------|
| **v1.1.0** | 2026-06-19 | Built-in `migrate` CLI, multi-format editor (Markdown ⇄ HTML), `article_sources` side-car, XML/HTML escaping fixes |
| **v1.0.0** | 2026-06-15 | Initial stable release (P1–P27 constitution, adaptive modes, write-queue, self-update, Admin v2) |
