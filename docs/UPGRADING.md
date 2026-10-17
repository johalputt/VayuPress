# Upgrading VayuPress

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

An upgrade replaces one file: the binary. Migrations, static assets and these
docs travel inside it. There are three ways to do it. Every one of them ends
the same way, and every one checks the same signatures first.

| Path | Who it is for | Restart |
|------|---------------|---------|
| **VayuOS → System › Updates** | Every install; one click | The console restarts the process for you |
| **`vayupress update apply`** | Scripts and operators who live in a shell | You restart |
| **The installer, `--upgrade`** | Building from source on the host | The installer restarts |

## What every release must prove

The updater installs nothing it cannot verify (`internal/update/apply.go`,
`ApplyVerified`). The order matters, so a missing control is reported before a
weaker one:

1. **A newer version exists**, from GitHub's releases API, the project's
   release mirror, or a CDN that can only *announce* a version. If only that
   CDN answered, the update is refused with the reason: it cannot serve the
   files.
2. **The release has an installable binary** for this platform, a `.sha256`,
   and a **Sigstore bundle**. No bundle, no install. An attacker who can publish
   a release can publish a matching checksum. Leaving out the signature must
   not wave them through.
3. **This build's release key has signed it too.** Builds since 3.17.63 carry
   an Ed25519 public key compiled in (`internal/update/releasekey.go`), so the
   release's `.sig` is required as a second lock. Holding the workflow identity
   does not yield the key, and holding the key does not let you publish under
   that identity.
4. **The SHA-256 matches.** This step catches a truncated or proxy-mangled
   download, and its error says so, so it is not mistaken for a bad release.
5. **The Sigstore signature** was made by this project's `tag-release.yml`
   workflow, checked against a trust root compiled into the binary.
6. **The bytes are a program this machine can run.** In the v3.16.86 outage a
   release was a ZIP archive that passed every check above it.

None of this is configurable. There is no flag that turns a signature off, and
no environment variable that adds one: the key is in the binary, so the policy
is the same on every install.

> **`VAYU_RELEASE_PUBKEY` does nothing.** Older versions of this guide told
> operators to set it. The release pipeline never produced the asset it looked
> for, so setting it broke every update, and leaving it unset meant nothing was
> checked at all. It was replaced by the compiled-in key. You can remove it from
> `/etc/vayupress/env`.

## From the console

**VayuOS → System › Updates** (`/os/update`).

- The install checks for a release two minutes after it starts and every six
  hours after that. It never checks from a Tor Space. When a release is
  waiting, the bell says so.
- **Check now** shows the version, its release notes, and which source
  answered.
- **Update** runs the steps above, then:
  - takes a VayuKeep restore point from just before the upgrade (ADR-0145);
  - backs up the database to `/var/cache/vayupress/update-backups/`, if
    *Back up the database first* is ticked;
  - swaps the binary atomically, keeping the old one as `<binary>.bak`;
  - re-execs the process, flushing the WAL first.
  On an install with socket activation, visitors wait during the restart
  instead of getting an error. The page shows how long a restart takes on
  this host.
- **Verify only** runs the same checks and writes nothing.
- **Roll back** puts `<binary>.bak` back and restarts.
- **History** lists every check, apply and rollback with its versions.

What is refused, and why:

| Refusal | Reason |
|---------|--------|
| A non-admin | Updates are admin-only; the request also needs its CSRF token |
| Mode is read-only, quarantined or maintenance | `update.PreflightMode`: those modes forbid changing the binary |
| The binary's directory is not writable by the service | The swap could not complete. The page names the directory and the fix. |
| Database over 2 GiB with the backup ticked | Compressing it inside the request can swap-thrash a small VPS. Untick the backup (the update does not touch the database), or take a VayuKeep snapshot first. |

Every action is written to the audit log and to the `update_history` table.

## From the shell

```bash
vayupress update check              # current, latest, release notes
vayupress update apply --dry-run    # download and verify; replaces nothing
vayupress update apply              # verify, back up the database, swap
vayupress update rollback           # put <binary>.bak back
vayupress update history            # the last 20 attempts
```

`apply` from the shell is opt-in. It refuses unless the environment it runs in
has `VAYU_SELFUPDATE_ENABLED=true`, and it refuses in the same modes the
console does. Run it as the service user, with the service's environment
(`/etc/vayupress/env` is readable by root only), then restart:

```bash
sudo systemd-run --uid=www-data --pipe --wait \
  -p EnvironmentFile=/etc/vayupress/env -E VAYU_SELFUPDATE_ENABLED=true \
  /usr/local/bin/vayupress update apply
sudo systemctl restart vayupress
```

The CLI never restarts the process; `rollback` likewise ends with
"Restart the service to run it."

## From source

```bash
cd /opt/vayupress && sudo git pull origin main
sudo ./scripts/deploy-vayupress.sh --upgrade
```

`--upgrade` keeps `/etc/vayupress/env`, the data and the generated secrets. It
installs the Go the module names, if the host's is older, then rebuilds and
restarts. A binary you build yourself is not signed. The updater only verifies
what it downloads.

## Migrations

Migrations are numbered SQL files compiled into the binary and applied at
start. Each applied migration's SHA-256 is recorded. A binary that finds a
recorded migration changed refuses to start rather than run on a schema it
does not recognise (ADR-0034).

A rolled-back binary starts on a database a newer one has migrated: the
checksum check skips migrations it does not know (`verifyMigrationChecksums`
in `internal/db/db.go`). If a newer migration changed something the older code
reads, restore the database from the backup the update took:

```bash
sudo systemctl stop vayupress
ls -t /var/cache/vayupress/update-backups/    # backup-vayupress.db-<time>.tar.gz
sudo mkdir -p /tmp/restore
sudo tar xzf /var/cache/vayupress/update-backups/<file> -C /tmp/restore
sudo cp /tmp/restore/vayupress.db /var/lib/vayupress/vayupress.db
sudo rm -f /var/lib/vayupress/vayupress.db-wal /var/lib/vayupress/vayupress.db-shm
sudo chown www-data:www-data /var/lib/vayupress/vayupress.db
sudo systemctl start vayupress
```

To roll back everything (database, media, mailboxes and keys) from a VayuKeep
generation or a `vayupress backup` file, see
[operations/backup-restore.md](operations/backup-restore.md).

## Before a major version

A minor or patch release never breaks a stable API route
([compatibility/api-contracts.md](compatibility/api-contracts.md)). Before a
major release:

1. Read its section in the [CHANGELOG](../CHANGELOG.md).
2. Take a full backup you can restore without the running install:

   ```bash
   sudo vayupress backup -out /root/before-upgrade.vpbk
   sudo vayupress restore -in /root/before-upgrade.vpbk -verify
   ```

3. Upgrade, then check `curl -s localhost:8080/health/ready` and the mode
   shown on every console page.

## Moving from another platform

Importing from WordPress, Ghost or Markdown is a migration, not an upgrade:
[MIGRATION.md](MIGRATION.md) and [GHOST-MIGRATION.md](GHOST-MIGRATION.md).
