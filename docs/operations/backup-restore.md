# Backup & Restore — VayuPress Operations

**Last reviewed:** 2026-10-16

An install keeps everything in one data directory, `/var/lib/vayupress` by
default: the SQLite database, settings, media, VayuMail's mailboxes and the PGP
key store. A backup is that directory, encrypted. There are two ways to take
one, and they write the same archive format.

---

## What a backup holds

| Item | Where on an install | In a backup |
|------|---------------------|-------------|
| Database (posts, members, settings, orders, audit log …) | `/var/lib/vayupress/vayupress.db` | Yes — a consistent snapshot, taken without stopping the service |
| Media library | `/var/lib/vayupress/media` | Yes |
| VayuMail mailboxes | `/var/lib/vayupress/vayudata/mail` | Yes |
| PGP key store (private keys encrypted at rest) | `/var/lib/vayupress/vayudata/pgp` | Yes |
| The page cache | `/var/cache/vayupress` | No — it is rebuilt from the database |
| Nginx, the systemd unit, `/etc/vayupress/env` | system files | No — the installer writes them; keep `/etc/vayupress/env` yourself |
| TLS certificates | certbot's store | No — certbot issues them again |

VayuOS → System › Backups lists anything the install keeps data in that a
backup would leave out, so a restore that "works" is never silently partial.

---

## Automatic: VayuKeep

**VayuKeep** takes backups inside the binary — no cron, no timer, no `sqlite3`
CLI. Set a target and a passphrase in VayuOS → System › Backups (or in
`/etc/vayupress/env`), and it runs.

```bash
VAYUKEEP_TARGET=/mnt/backup/vayupress   # anywhere outside the data directory
VAYU_BACKUP_PASSPHRASE=…                # the same passphrase `vayupress backup` uses
```

| What | How often | Kept |
|------|-----------|------|
| A generation of everything above | From every 5 minutes while the site is being written to, backing off to every 6 hours when idle | 24 generations **or** 30 days, whichever keeps more |
| A restore drill | Every 12 hours, and on demand | The last result |

- **Consistent without stopping.** Each generation's database is copied with
  SQLite's backup API over one pinned read transaction, so it is one moment's
  data. The copy, the encryption and the test restore go in paced steps and
  slow down while visitors need the disk (`internal/sqlitecopy`,
  `internal/vayukeep`).
- **Encrypted, with no other option.** AES-256-GCM under an Argon2id-wrapped
  key, each frame chained to the last, ending in an authenticated final
  marker. Nothing is written unencrypted.
- **Proven by reading back.** The drill restores the newest generation into a
  scratch directory and checks every table. The figure to watch on System ›
  Backups is *last verified restore*, not *enabled*.

Setting a target is the intent to replicate, so there is no second switch to
forget. `VAYUKEEP_OFF=true` turns it off explicitly. Design: ADR-0145.

---

## By hand: `vayupress backup`

```bash
# A whole-install backup, taken while the service runs
sudo vayupress backup -out /root/vayupress-$(date +%F).vpbk

# Read the passphrase from a file instead of the prompt
sudo vayupress backup -out site.vpbk -passphrase-file /root/backup.pass
```

The passphrase is never read from the command line, so it does not land in
shell history or `ps`. Without it the file cannot be read by any tool — store
the two apart.

---

## Restore

Always verify an archive before trusting it. `-verify` checks the passphrase,
every authentication tag, the chain between frames and the final marker, and
writes nothing:

```bash
vayupress restore -in /mnt/backup/vayupress/vk-20261016-104500.vpbk -verify
```

Then restore:

```bash
sudo systemctl stop vayupress
sudo vayupress restore -in /mnt/backup/vayupress/vk-20261016-104500.vpbk -dest /var/lib/vayupress
sudo chown -R www-data:www-data /var/lib/vayupress   # the service runs as www-data
sudo systemctl start vayupress
curl -s http://localhost:8080/health
```

- **Nothing is deleted.** The directory you replaced is moved aside, and the
  restore prints where (`/var/lib/vayupress.replaced-<timestamp>`). Keep it
  until you have confirmed the restored generation is the one you wanted.
- **A failed restore changes nothing.** Entries are staged in a sibling
  directory and moved into place only after the archive reaches its
  authenticated final marker; a truncated or tampered archive is refused
  before the live directory is touched.
- **From the console.** VayuOS → System › Backups can also import an archive;
  the restore is staged and applied atomically on the next start, with a
  safety copy of the current database taken first.

Choosing a generation by time, and what to do when a drill fails, are
DR-06 and DR-07 in [disaster-recovery.md](disaster-recovery.md).
